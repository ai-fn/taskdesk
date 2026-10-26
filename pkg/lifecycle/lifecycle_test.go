package lifecycle

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/grpc/health"
	"google.golang.org/grpc/health/grpc_health_v1"
)

func quiet() *slog.Logger { return slog.New(slog.NewTextHandler(io.Discard, nil)) }

func ok(context.Context) error { return nil }

func TestRun_JoinsFailuresAndBoundsSlowChecks(t *testing.T) {
	boom := errors.New("down")
	err := Run(context.Background(), time.Second,
		Check{"db", func(context.Context) error { return boom }},
		Check{"redis", ok},
		Check{"todo", func(context.Context) error { return errors.New("slow") }},
	)
	if err == nil || !errors.Is(err, boom) || !strings.Contains(err.Error(), "db: down") || !strings.Contains(err.Error(), "todo: slow") || strings.Contains(err.Error(), "redis") {
		t.Errorf("err = %v; want db and todo failures only", err)
	}

	start := time.Now()
	err = Run(context.Background(), 50*time.Millisecond, Check{"hang", func(ctx context.Context) error { <-ctx.Done(); return ctx.Err() }})
	if !errors.Is(err, context.DeadlineExceeded) || time.Since(start) > time.Second {
		t.Errorf("hung check: err %v after %v; want a deadline error well under 1s", err, time.Since(start))
	}
}

func readiness(t *testing.T, srv *health.Server) grpc_health_v1.HealthCheckResponse_ServingStatus {
	t.Helper()
	resp, err := srv.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{Service: ReadinessService})
	if err != nil {
		t.Fatal(err)
	}
	return resp.Status
}

func TestWatchGRPC_FollowsTheDependency(t *testing.T) {
	var down atomic.Bool
	srv := health.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	WatchGRPC(ctx, srv, 10*time.Millisecond, time.Second, quiet(), Check{"db", func(context.Context) error {
		if down.Load() {
			return errors.New("ping failed")
		}
		return nil
	}})
	if got := readiness(t, srv); got != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Fatalf("initial = %s, want SERVING set before WatchGRPC returns", got)
	}
	wait := func(want grpc_health_v1.HealthCheckResponse_ServingStatus) {
		t.Helper()
		for range 200 {
			if readiness(t, srv) == want {
				return
			}
			time.Sleep(10 * time.Millisecond)
		}
		t.Fatalf("readiness never became %s", want)
	}
	down.Store(true)
	wait(grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	down.Store(false)
	wait(grpc_health_v1.HealthCheckResponse_SERVING)

	// Liveness (the default service) is independent of dependencies.
	live, _ := srv.Check(context.Background(), &grpc_health_v1.HealthCheckRequest{})
	if live != nil && live.Status != grpc_health_v1.HealthCheckResponse_SERVING {
		t.Errorf("liveness = %s, want SERVING", live.Status)
	}
}

func TestWatchGRPC_OneFailedPollDoesNotUnrouteAReadyService(t *testing.T) {
	var polls atomic.Int32
	srv := health.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	WatchGRPC(ctx, srv, 5*time.Millisecond, time.Second, quiet(), Check{"db", func(context.Context) error {
		if polls.Add(1) == 2 { // a single slow/failed poll between healthy ones
			return errors.New("timeout")
		}
		return nil
	}})
	for polls.Load() < 20 {
		if got := readiness(t, srv); got != grpc_health_v1.HealthCheckResponse_SERVING {
			t.Fatalf("flipped to %s on one failed poll", got)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestWatchGRPC_FirstFailureIsNotServing(t *testing.T) {
	srv := health.NewServer()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	WatchGRPC(ctx, srv, time.Hour, time.Second, quiet(), Check{"db", func(context.Context) error { return errors.New("down") }})
	if got := readiness(t, srv); got != grpc_health_v1.HealthCheckResponse_NOT_SERVING {
		t.Errorf("status = %s, want NOT_SERVING from the first check", got)
	}
}

func serve(t *testing.T, srv *grpc.Server) *grpc.ClientConn {
	t.Helper()
	lis, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(lis) }()
	t.Cleanup(srv.Stop)
	conn, err := grpc.NewClient(lis.Addr().String(), grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = conn.Close() })
	return conn
}

func TestGRPCCheck(t *testing.T) {
	hs := health.NewServer()
	gs := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(gs, hs)
	conn := serve(t, gs)
	c := GRPCCheck("upstream", conn)

	hs.SetServingStatus(ReadinessService, grpc_health_v1.HealthCheckResponse_SERVING)
	if err := Run(context.Background(), time.Second, c); err != nil {
		t.Errorf("serving upstream: %v", err)
	}
	hs.SetServingStatus(ReadinessService, grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	if err := Run(context.Background(), time.Second, c); err == nil {
		t.Error("NOT_SERVING upstream passed")
	}
	// A "" (liveness) SERVING must not satisfy a readiness check.
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_SERVING)
	if err := Run(context.Background(), time.Second, c); err == nil {
		t.Error("liveness status leaked into readiness")
	}
	gs.Stop()
	if err := Run(context.Background(), time.Second, c); err == nil {
		t.Error("stopped upstream passed")
	}
}

// A pre-readiness upstream (rollback or version skew) answers NotFound for "readiness".
func TestGRPCCheck_FallsBackToLivenessWhenReadinessIsUnknown(t *testing.T) {
	hs := health.NewServer() // registers only the default "" service
	gs := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(gs, hs)
	c := GRPCCheck("old-upstream", serve(t, gs))

	if err := Run(context.Background(), time.Second, c); err != nil {
		t.Errorf("old upstream serving liveness: %v, want ready", err)
	}
	hs.SetServingStatus("", grpc_health_v1.HealthCheckResponse_NOT_SERVING)
	if err := Run(context.Background(), time.Second, c); err == nil {
		t.Error("old upstream with failing liveness passed")
	}
}

func TestGracefulStop_ForcesAfterTimeoutWithAHungStream(t *testing.T) {
	hs := health.NewServer()
	gs := grpc.NewServer()
	grpc_health_v1.RegisterHealthServer(gs, hs)
	conn := serve(t, gs)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	stream, err := grpc_health_v1.NewHealthClient(conn).Watch(ctx, &grpc_health_v1.HealthCheckRequest{})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := stream.Recv(); err != nil { // stream is established and stays open
		t.Fatal(err)
	}

	start := time.Now()
	if GracefulStop(gs, 100*time.Millisecond, quiet()) {
		t.Error("reported a clean drain with a stream still open")
	}
	if d := time.Since(start); d > 2*time.Second {
		t.Errorf("stop took %v; the hung stream must not block past the timeout", d)
	}
}

func TestGracefulStop_CleanDrain(t *testing.T) {
	gs := grpc.NewServer()
	serve(t, gs)
	if !GracefulStop(gs, time.Second, quiet()) {
		t.Error("idle server did not drain cleanly")
	}
}

func TestHandlers(t *testing.T) {
	var fail atomic.Bool
	h := ReadyHandler(time.Second, quiet(), Check{"db", func(context.Context) error {
		if fail.Load() {
			return errors.New("secret detail")
		}
		return nil
	}})
	get := func() *httptest.ResponseRecorder {
		w := httptest.NewRecorder()
		h(w, httptest.NewRequest(http.MethodGet, "/readyz", nil))
		return w
	}
	if w := get(); w.Code != 200 {
		t.Errorf("ready: %d", w.Code)
	}
	fail.Store(true)
	if w := get(); w.Code != http.StatusServiceUnavailable || strings.Contains(w.Body.String(), "secret") {
		t.Errorf("not ready: %d %q; want 503 without internals", w.Code, w.Body.String())
	}
	w := httptest.NewRecorder()
	LiveHandler()(w, httptest.NewRequest(http.MethodGet, "/healthz", nil))
	if w.Code != 200 {
		t.Errorf("live: %d", w.Code)
	}
}
