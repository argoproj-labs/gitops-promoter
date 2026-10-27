package metrics

import (
	"fmt"
	"net/http"
	"time"

	"github.com/go-logr/logr"
	"k8s.io/client-go/rest"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"
)

// ScrapeLogFilterProvider returns a metrics FilterProvider that logs each HTTP request
// to the metrics server (including /metrics and ExtraHandlers) at info level.
func ScrapeLogFilterProvider() func(*rest.Config, *http.Client) (metricsserver.Filter, error) {
	return func(*rest.Config, *http.Client) (metricsserver.Filter, error) {
		return func(log logr.Logger, h http.Handler) (http.Handler, error) {
			return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				start := time.Now()
				sw := &statusCapturingWriter{ResponseWriter: w, status: http.StatusOK}
				h.ServeHTTP(sw, r)
				log.Info("metrics HTTP request",
					"method", r.Method,
					"remoteAddr", r.RemoteAddr,
					"userAgent", r.UserAgent(),
					"status", sw.status,
					"duration", time.Since(start),
				)
			}), nil
		}, nil
	}
}

type statusCapturingWriter struct {
	http.ResponseWriter
	status int
}

func (w *statusCapturingWriter) WriteHeader(code int) {
	w.status = code
	w.ResponseWriter.WriteHeader(code)
}

// WithScrapeLog wraps the filter from inner in the scrape-logging filter, so requests that inner
// rejects (e.g. a 401 or 403 from authn/authz) are still logged.
func WithScrapeLog(inner func(*rest.Config, *http.Client) (metricsserver.Filter, error)) func(*rest.Config, *http.Client) (metricsserver.Filter, error) {
	return func(c *rest.Config, httpClient *http.Client) (metricsserver.Filter, error) {
		innerFilter, err := inner(c, httpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to build inner metrics filter: %w", err)
		}
		logFilter, err := ScrapeLogFilterProvider()(c, httpClient)
		if err != nil {
			return nil, fmt.Errorf("failed to build scrape log filter: %w", err)
		}
		return func(log logr.Logger, h http.Handler) (http.Handler, error) {
			innerHandler, err := innerFilter(log, h)
			if err != nil {
				return nil, fmt.Errorf("failed to wrap metrics handler: %w", err)
			}
			return logFilter(log, innerHandler)
		}, nil
	}
}
