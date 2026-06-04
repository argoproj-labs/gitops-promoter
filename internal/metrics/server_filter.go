package metrics

import (
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
