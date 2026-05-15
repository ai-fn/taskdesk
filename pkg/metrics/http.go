package metrics

import (
	"net/http"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
)

var (
	httpRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "http_requests_total",
		Help: "Total number of HTTP requests by method, path, and status code.",
	}, []string{"method", "path", "code"})

	httpDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "http_duration_seconds",
		Help:    "HTTP request duration in seconds.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"method", "path", "code"})
)

// HTTPMiddleware records RED metrics for every HTTP request.
// Use pattern as the path label (e.g. "/api/v1/tasks") to avoid high cardinality.
func HTTPMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
		next.ServeHTTP(rw, r)
		dur := time.Since(start).Seconds()
		code := strconv.Itoa(rw.code)
		httpRequestsTotal.WithLabelValues(r.Method, r.URL.Path, code).Inc()
		httpDurationSeconds.WithLabelValues(r.Method, r.URL.Path, code).Observe(dur)
	})
}

type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}
