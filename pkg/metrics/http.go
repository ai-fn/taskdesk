package metrics

import (
	"bufio"
	"net"
	"net/http"
	"strconv"
	"strings"
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

// Unmatched labels requests that hit no route, so scanners can't mint series.
const Unmatched = "unmatched"

// HTTPMiddleware records RED metrics for every HTTP request. route returns the
// matched route pattern once the handler has run; empty means no route matched.
func HTTPMiddleware(route func(*http.Request) string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			start := time.Now()
			rw := &responseWriter{ResponseWriter: w, code: http.StatusOK}
			next.ServeHTTP(rw, r)
			dur := time.Since(start).Seconds()
			path := route(r)
			// A mount's catch-all 404 is also unmatched; so are requests rejected before routing.
			if path == "" || (rw.code == http.StatusNotFound && strings.HasSuffix(path, "/*")) {
				path = Unmatched
			}
			method, code := methodLabel(r.Method), strconv.Itoa(rw.code)
			httpRequestsTotal.WithLabelValues(method, path, code).Inc()
			httpDurationSeconds.WithLabelValues(method, path, code).Observe(dur)
		})
	}
}

// methodLabel bounds the method label: the server accepts any method token.
func methodLabel(m string) string {
	switch m {
	case http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
		http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace:
		return m
	}
	return "OTHER"
}

type responseWriter struct {
	http.ResponseWriter
	code int
}

func (rw *responseWriter) WriteHeader(code int) {
	rw.code = code
	rw.ResponseWriter.WriteHeader(code)
}

func (rw *responseWriter) Flush() {
	if f, ok := rw.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (rw *responseWriter) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	h, ok := rw.ResponseWriter.(http.Hijacker)
	if !ok {
		return nil, nil, http.ErrNotSupported
	}
	return h.Hijack()
}
