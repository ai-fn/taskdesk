package server

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/fsociety/todo/pkg/lifecycle"
	"github.com/redis/go-redis/v9"
)

func TestProbesReflectReadinessAndAreNeverRateLimited(t *testing.T) {
	var down atomic.Bool
	rdb := redis.NewClient(&redis.Options{Addr: "127.0.0.1:1"})
	h, cleanup := New(slog.New(slog.NewTextHandler(io.Discard, nil)), nil, nil, nil, rdb, nil, false,
		lifecycle.Check{Name: "auth-service", Fn: func(context.Context) error {
			if down.Load() {
				return errors.New("unavailable")
			}
			return nil
		}})
	t.Cleanup(func() { cleanup(context.Background()); _ = rdb.Close() })

	get := func(path string) int {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		req.RemoteAddr = "10.0.0.1:1234"
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w.Code
	}
	for range 500 { // beyond the per-IP burst of 200
		if got := get("/readyz"); got != http.StatusOK {
			t.Fatalf("/readyz = %d while ready (rate limited?)", got)
		}
		if got := get("/healthz"); got != http.StatusOK {
			t.Fatalf("/healthz = %d", got)
		}
	}
	down.Store(true)
	if got := get("/readyz"); got != http.StatusServiceUnavailable {
		t.Errorf("/readyz = %d with a failing dependency, want 503", got)
	}
	if got := get("/healthz"); got != http.StatusOK {
		t.Errorf("/healthz = %d; liveness must not depend on upstreams", got)
	}
}
