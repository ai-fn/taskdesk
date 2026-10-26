// Package lifecycle holds the readiness and shutdown plumbing shared by every service.
package lifecycle

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"sync"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
	"google.golang.org/grpc/status"
)

// ReadinessService is the gRPC health service name behind k8s readiness probes. Liveness uses
// the default "" service, which stays SERVING so a dependency outage never restarts pods.
const ReadinessService = "readiness"

// Defaults shared by all services: readiness poll interval, per-poll timeout, and the drain
// budget on SIGTERM (k8s grants 30 s before SIGKILL).
const (
	ProbeInterval   = 5 * time.Second
	ProbeTimeout    = 2 * time.Second
	ShutdownTimeout = 5 * time.Second
)

// failuresToFlip is the consecutive failed polls before a ready service turns NOT_SERVING.
const failuresToFlip = 2

// Check is one readiness dependency.
type Check struct {
	Name string
	Fn   func(ctx context.Context) error
}

// Run executes the checks concurrently under one timeout and joins their failures.
func Run(ctx context.Context, timeout time.Duration, checks ...Check) error {
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	errs := make([]error, len(checks))
	var wg sync.WaitGroup
	for i, c := range checks {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := c.Fn(ctx); err != nil {
				errs[i] = fmt.Errorf("%s: %w", c.Name, err)
			}
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// GRPCCheck is a Check on another service's readiness through the standard health protocol.
// A connection stays pinned to one upstream pod (pick_first), so with several replicas this
// reflects that pod only; use a headless Service with round_robin before relying on it.
func GRPCCheck(name string, conn grpc.ClientConnInterface) Check {
	client := grpc_health_v1.NewHealthClient(conn)
	return Check{Name: name, Fn: func(ctx context.Context) error {
		resp, err := client.Check(ctx, &grpc_health_v1.HealthCheckRequest{Service: ReadinessService})
		if status.Code(err) == codes.NotFound { // pre-readiness binary (rollback/skew): fall back to liveness
			resp, err = client.Check(ctx, &grpc_health_v1.HealthCheckRequest{})
		}
		if err != nil {
			return err
		}
		if resp.Status != grpc_health_v1.HealthCheckResponse_SERVING {
			return fmt.Errorf("status %s", resp.Status)
		}
		return nil
	}}
}

// WatchGRPC mirrors the checks into srv's ReadinessService until ctx ends. The first result is
// set before it returns, so the server never reports a stale SERVING at startup; afterwards a
// ready service needs failuresToFlip failed polls in a row, so one slow ping under load doesn't unroute it.
func WatchGRPC(ctx context.Context, srv *health.Server, every, timeout time.Duration, log *slog.Logger, checks ...Check) {
	ready, fails, first := true, 0, true
	apply := func() {
		err := Run(ctx, timeout, checks...)
		if ctx.Err() != nil {
			return
		}
		if err == nil {
			fails = 0
		} else {
			fails++
		}
		if err != nil && ready && !first && fails < failuresToFlip {
			return
		}
		first = false
		st := grpc_health_v1.HealthCheckResponse_SERVING
		if err != nil {
			st = grpc_health_v1.HealthCheckResponse_NOT_SERVING
		}
		srv.SetServingStatus(ReadinessService, st)
		switch {
		case err != nil && ready:
			log.Warn("not ready", "err", err)
		case err == nil && !ready:
			log.Info("ready again")
		}
		ready = err == nil
	}
	apply()
	go func() {
		t := time.NewTicker(every)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				apply()
			}
		}
	}()
}

// ReadyHandler answers 200 when every check passes and 503 otherwise; failures are logged, not echoed.
func ReadyHandler(timeout time.Duration, log *slog.Logger, checks ...Check) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if err := Run(r.Context(), timeout, checks...); err != nil {
			log.Warn("readyz failed", "err", err)
			http.Error(w, "not ready", http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte("ok"))
	}
}

// LiveHandler answers 200 while the process can serve a request at all.
func LiveHandler() http.HandlerFunc {
	return func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) }
}

// GracefulStop drains srv for at most timeout, then force-stops it so one hung stream can't
// block shutdown until SIGKILL. It reports whether the drain finished in time.
func GracefulStop(srv *grpc.Server, timeout time.Duration, log *slog.Logger) bool {
	done := make(chan struct{})
	go func() {
		srv.GracefulStop()
		close(done)
	}()
	t := time.NewTimer(timeout)
	defer t.Stop()
	select {
	case <-done:
		return true
	case <-t.C:
		log.Warn("graceful stop timed out, forcing", "timeout", timeout)
		srv.Stop()
		<-done
		return false
	}
}
