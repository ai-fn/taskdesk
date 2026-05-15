// Package metrics provides Prometheus instrumentation and gRPC server interceptors.
package metrics

import (
	"context"
	"log/slog"
	"net/http"
	"time"

	pkglogger "github.com/fsociety/todo/pkg/logger"
	pkgtracing "github.com/fsociety/todo/pkg/tracing"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"google.golang.org/grpc"
	"google.golang.org/grpc/status"
)

var (
	grpcRequestsTotal = promauto.NewCounterVec(prometheus.CounterOpts{
		Name: "grpc_requests_total",
		Help: "Total number of gRPC requests by service, method, and status code.",
	}, []string{"service", "method", "code"})

	grpcDurationSeconds = promauto.NewHistogramVec(prometheus.HistogramOpts{
		Name:    "grpc_duration_seconds",
		Help:    "gRPC request duration in seconds.",
		Buckets: []float64{.001, .005, .01, .025, .05, .1, .25, .5, 1, 2.5, 5},
	}, []string{"service", "method", "code"})
)

// Handler returns an HTTP handler that serves Prometheus metrics.
func Handler() http.Handler {
	return promhttp.Handler()
}

// UnaryServerInterceptor returns a gRPC unary interceptor that:
//   - extracts or generates a trace ID, stores it in context and logger
//   - records request count and latency in Prometheus
func UnaryServerInterceptor(svcName string, log *slog.Logger) grpc.UnaryServerInterceptor {
	return func(ctx context.Context, req any, info *grpc.UnaryServerInfo, handler grpc.UnaryHandler) (any, error) {
		traceID, ok := pkgtracing.ExtractGRPC(ctx)
		if !ok || traceID == "" {
			traceID = pkgtracing.New()
		}
		ctx = pkgtracing.WithID(ctx, traceID)
		// Inject trace-enriched logger so handlers can use pkglogger.FromContext.
		ctx = pkglogger.Into(ctx, log.With("trace_id", traceID))

		start := time.Now()
		resp, err := handler(ctx, req)

		code := status.Code(err).String()
		dur := time.Since(start).Seconds()
		grpcRequestsTotal.WithLabelValues(svcName, info.FullMethod, code).Inc()
		grpcDurationSeconds.WithLabelValues(svcName, info.FullMethod, code).Observe(dur)

		return resp, err
	}
}

// StreamServerInterceptor returns a gRPC stream interceptor that records
// metrics and injects a trace ID into the stream context.
func StreamServerInterceptor(svcName string, log *slog.Logger) grpc.StreamServerInterceptor {
	return func(srv any, ss grpc.ServerStream, info *grpc.StreamServerInfo, handler grpc.StreamHandler) error {
		ctx := ss.Context()
		traceID, ok := pkgtracing.ExtractGRPC(ctx)
		if !ok || traceID == "" {
			traceID = pkgtracing.New()
		}
		ctx = pkgtracing.WithID(ctx, traceID)
		ctx = pkglogger.Into(ctx, log.With("trace_id", traceID))
		ss = &wrappedStream{ServerStream: ss, ctx: ctx}

		start := time.Now()
		err := handler(srv, ss)

		code := status.Code(err).String()
		dur := time.Since(start).Seconds()
		grpcRequestsTotal.WithLabelValues(svcName, info.FullMethod, code).Inc()
		grpcDurationSeconds.WithLabelValues(svcName, info.FullMethod, code).Observe(dur)

		return err
	}
}

// wrappedStream overrides Context() so the enriched ctx is visible to handlers.
type wrappedStream struct {
	grpc.ServerStream
	ctx context.Context
}

func (w *wrappedStream) Context() context.Context { return w.ctx }
