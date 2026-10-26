package handler_test

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
)

func TestResetPassword_GuessesAreCappedPerEmailAcrossIPs(t *testing.T) {
	calls := 0
	fake := &fakeAuthClient{resetPassword: func(context.Context, *authv1.ResetPasswordRequest, ...grpc.CallOption) (*authv1.ResetPasswordResponse, error) {
		calls++
		return &authv1.ResetPasswordResponse{}, nil
	}}
	h := handler.NewAuthHandler(fake, nil, slog.New(slog.NewTextHandler(io.Discard, nil)))
	defer h.Stop()
	r := chi.NewRouter()
	r.Post("/api/v1/auth/password/reset", h.ResetPassword)

	post := func(email string, ip int) int {
		body := fmt.Sprintf(`{"email":%q,"code":"000000","new_password":"Passw0rd1"}`, email)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/reset", strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.0.0.%d", ip))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}

	// Case and spacing variants share one budget; a fresh IP each time dodges the IP limiter.
	for i := range 10 {
		email := []string{"Victim@Example.com", " victim@example.com", "VICTIM@EXAMPLE.COM"}[i%3]
		if code := post(email, i+1); code != http.StatusNoContent {
			t.Fatalf("guess %d: status %d, want 204", i+1, code)
		}
	}
	if code := post("victim@example.com", 50); code != http.StatusTooManyRequests {
		t.Errorf("11th guess: status %d, want 429", code)
	}
	if calls != 10 {
		t.Errorf("auth-service saw %d guesses, want 10", calls)
	}
	if code := post("someone-else@example.com", 51); code != http.StatusNoContent {
		t.Errorf("other account: status %d, want 204", code)
	}
}
