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
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func TestResetPassword_GuessesAreCappedPerEmailAcrossIPs(t *testing.T) {
	calls := 0
	fake := &fakeAuthClient{resetPassword: func(context.Context, *authv1.ResetPasswordRequest, ...grpc.CallOption) (*authv1.ResetPasswordResponse, error) {
		calls++
		return &authv1.ResetPasswordResponse{}, nil
	}}
	h := handler.NewAuthHandler(fake, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
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

func TestLogin_EmailCaseVariantsShareOneLimiter(t *testing.T) {
	calls := 0
	fake := &fakeAuthClient{login: func(context.Context, *authv1.LoginRequest, ...grpc.CallOption) (*authv1.LoginResponse, error) {
		calls++
		return nil, status.Error(codes.Unauthenticated, "invalid email or password")
	}}
	h := handler.NewAuthHandler(fake, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	defer h.Stop()
	r := chi.NewRouter()
	r.Post("/api/v1/auth/login", h.Login)

	var last int
	for i := range 6 {
		email := []string{"Target@X.com", "target@x.com", "TARGET@X.COM"}[i%3]
		body := fmt.Sprintf(`{"email":%q,"password":"guess%d"}`, email, i)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(body))
		req.Header.Set("X-Forwarded-For", fmt.Sprintf("10.1.0.%d", i+1))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		last = w.Code
	}
	if last != http.StatusTooManyRequests || calls != 5 {
		t.Errorf("6th guess: status=%d, backend calls=%d; want 429 and 5 (case variants must not dodge the 5/min cap)", last, calls)
	}
}

// One /64 is one budget: varying the low bits of an IPv6 address must not mint fresh buckets.
func TestResetPassword_IPv6LimiterIsPerSlash64(t *testing.T) {
	fake := &fakeAuthClient{resetPassword: func(context.Context, *authv1.ResetPasswordRequest, ...grpc.CallOption) (*authv1.ResetPasswordResponse, error) {
		return &authv1.ResetPasswordResponse{}, nil
	}}
	h := handler.NewAuthHandler(fake, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), false)
	defer h.Stop()
	r := chi.NewRouter()
	r.Post("/api/v1/auth/password/reset", h.ResetPassword)

	post := func(n int) int {
		body := fmt.Sprintf(`{"email":"user%d@example.com","code":"000000","new_password":"Passw0rd1"}`, n)
		req := httptest.NewRequest(http.MethodPost, "/api/v1/auth/password/reset", strings.NewReader(body))
		req.Header.Set("X-Real-IP", fmt.Sprintf("2001:db8:1:2::%x", n+1))
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)
		return w.Code
	}
	for i := range 10 {
		if code := post(i); code != http.StatusNoContent {
			t.Fatalf("request %d: status %d, want 204", i+1, code)
		}
	}
	if code := post(10); code != http.StatusTooManyRequests {
		t.Errorf("11th request from the same /64: status %d, want 429", code)
	}
}
