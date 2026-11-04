package metrics

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/go-logr/logr"
	"github.com/go-logr/logr/funcr"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// capturingLogger is a logr.Logger backed by funcr that records every Info() call so tests can
// assert on what was logged, without depending on any real logging backend.
type capturingLogger struct {
	lines []string
	mu    sync.Mutex
}

func (c *capturingLogger) logger() logr.Logger {
	return funcr.New(func(prefix, args string) {
		c.mu.Lock()
		defer c.mu.Unlock()
		c.lines = append(c.lines, strings.TrimSpace(prefix+" "+args))
	}, funcr.Options{})
}

func (c *capturingLogger) joined() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	return strings.Join(c.lines, "\n")
}

// stubFilterProvider builds a metricsserver FilterProvider for use as the "inner" (e.g.
// authn/authz) filter in WithScrapeLog tests:
//   - buildErr, if non-nil, makes the provider itself fail (simulates filters.WithAuthenticationAndAuthorization
//     failing to construct, e.g. bad TLS/kubeconfig).
//   - handlerErr, if non-nil, makes building the handler (the Filter func) fail.
//   - replace, if non-nil, is returned instead of the wrapped handler - simulating a filter that
//     rejects the request itself (e.g. a 401/403) rather than forwarding it. If nil, the filter
//     forwards straight through to the handler it was given, as an authorized request would.
func stubFilterProvider(buildErr, handlerErr error, replace http.Handler) func(*rest.Config, *http.Client) (metricsserver.Filter, error) {
	return func(*rest.Config, *http.Client) (metricsserver.Filter, error) {
		if buildErr != nil {
			return nil, buildErr
		}
		return func(_ logr.Logger, h http.Handler) (http.Handler, error) {
			if handlerErr != nil {
				return nil, handlerErr
			}
			if replace != nil {
				return replace, nil
			}
			return h, nil
		}, nil
	}
}

func TestWithScrapeLog_InnerProviderBuildError(t *testing.T) {
	t.Parallel()
	inner := stubFilterProvider(errors.New("boom"), nil, nil)

	_, err := WithScrapeLog(inner)(nil, nil)
	if err == nil {
		t.Fatal("got nil error, want an error wrapping the inner provider's failure")
	}
	if !strings.Contains(err.Error(), "failed to build inner metrics filter") || !strings.Contains(err.Error(), "boom") {
		t.Errorf("error = %q, want it to mention %q and wrap %q", err, "failed to build inner metrics filter", "boom")
	}
}

func TestWithScrapeLog_InnerHandlerBuildError(t *testing.T) {
	t.Parallel()
	inner := stubFilterProvider(nil, errors.New("denied"), nil)

	filter, err := WithScrapeLog(inner)(nil, nil)
	if err != nil {
		t.Fatalf("WithScrapeLog(...)(nil, nil) error = %v, want nil (the provider itself should succeed)", err)
	}

	_, err = filter(logr.Discard(), http.NotFoundHandler())
	if err == nil {
		t.Fatal("got nil error, want an error wrapping the inner filter's failure to build a handler")
	}
	if !strings.Contains(err.Error(), "failed to wrap metrics handler") || !strings.Contains(err.Error(), "denied") {
		t.Errorf("error = %q, want it to mention %q and wrap %q", err, "failed to wrap metrics handler", "denied")
	}
}

func TestWithScrapeLog_AuthorizedRequestPassesThroughAndIsLogged(t *testing.T) {
	t.Parallel()
	var downstreamCalled bool
	downstream := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		downstreamCalled = true
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})

	// inner forwards straight through, as it would for an authorized request.
	inner := stubFilterProvider(nil, nil, nil)
	filter, err := WithScrapeLog(inner)(nil, nil)
	if err != nil {
		t.Fatalf("WithScrapeLog(...)(nil, nil) error = %v", err)
	}

	logs := &capturingLogger{}
	handler, err := filter(logs.logger(), downstream)
	if err != nil {
		t.Fatalf("filter(...) error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)

	if !downstreamCalled {
		t.Error("downstream handler was not called, want the authorized request to reach it")
	}
	if rec.Code != http.StatusOK {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusOK)
	}
	if rec.Body.String() != "ok" {
		t.Errorf("response body = %q, want %q", rec.Body.String(), "ok")
	}

	logged := logs.joined()
	if !strings.Contains(logged, "metrics HTTP request") {
		t.Errorf("logs = %q, want it to contain the scrape-log message", logged)
	}
	if !strings.Contains(logged, `"method"="GET"`) {
		t.Errorf("logs = %q, want it to record the request method", logged)
	}
	if !strings.Contains(logged, `"status"=200`) {
		t.Errorf("logs = %q, want it to record the response status", logged)
	}
}

func TestWithScrapeLog_RejectedRequestIsStillLogged(t *testing.T) {
	t.Parallel()
	var downstreamCalled bool
	downstream := http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		downstreamCalled = true
	})
	reject := http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusForbidden)
	})

	// inner rejects the request itself (e.g. a 403 from authn/authz) instead of forwarding it.
	inner := stubFilterProvider(nil, nil, reject)
	filter, err := WithScrapeLog(inner)(nil, nil)
	if err != nil {
		t.Fatalf("WithScrapeLog(...)(nil, nil) error = %v", err)
	}

	logs := &capturingLogger{}
	handler, err := filter(logs.logger(), downstream)
	if err != nil {
		t.Fatalf("filter(...) error = %v", err)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
	handler.ServeHTTP(rec, req)

	if downstreamCalled {
		t.Error("downstream handler was called, want the rejected request to never reach it")
	}
	if rec.Code != http.StatusForbidden {
		t.Errorf("response status = %d, want %d", rec.Code, http.StatusForbidden)
	}

	// This is the whole point of WithScrapeLog, per its doc comment: a request that the inner
	// (authn/authz) filter rejects before ever reaching the real handler must still show up in
	// the scrape log, status and all.
	logged := logs.joined()
	if !strings.Contains(logged, "metrics HTTP request") {
		t.Errorf("logs = %q, want the rejected request to be logged anyway", logged)
	}
	if !strings.Contains(logged, `"status"=403`) {
		t.Errorf("logs = %q, want it to record the rejection status", logged)
	}
}

// TestScrapeLogFilterProvider covers the filter WithScrapeLog itself builds on - logging every
// request's method/status regardless of whether it's /metrics or an ExtraHandler, and defaulting
// to 200 for handlers that never explicitly call WriteHeader.
func TestScrapeLogFilterProvider(t *testing.T) {
	t.Parallel()

	tests := []struct {
		downstream http.HandlerFunc
		name       string
		wantStatus int
	}{
		{
			name: "explicit status is captured",
			downstream: func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(http.StatusTeapot)
			},
			wantStatus: http.StatusTeapot,
		},
		{
			name: "no explicit WriteHeader defaults to 200",
			downstream: func(w http.ResponseWriter, _ *http.Request) {
				_, _ = w.Write([]byte("ok"))
			},
			wantStatus: http.StatusOK,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			provider, err := ScrapeLogFilterProvider()(nil, nil)
			if err != nil {
				t.Fatalf("ScrapeLogFilterProvider()(nil, nil) error = %v", err)
			}

			logs := &capturingLogger{}
			handler, err := provider(logs.logger(), tt.downstream)
			if err != nil {
				t.Fatalf("provider(...) error = %v", err)
			}

			rec := httptest.NewRecorder()
			req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/metrics", nil)
			handler.ServeHTTP(rec, req)

			if rec.Code != tt.wantStatus {
				t.Errorf("response status = %d, want %d", rec.Code, tt.wantStatus)
			}

			wantLogged := "\"status\"=" + strconv.Itoa(tt.wantStatus)
			if logged := logs.joined(); !strings.Contains(logged, wantLogged) {
				t.Errorf("logs = %q, want it to contain %q", logged, wantLogged)
			}
		})
	}
}
