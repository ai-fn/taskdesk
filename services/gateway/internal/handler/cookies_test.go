package handler_test

import (
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func cookieRouter(fake *fakeAuthClient, secure bool) http.Handler {
	h := handler.NewAuthHandler(fake, nil, slog.New(slog.NewTextHandler(io.Discard, nil)), secure)
	r := chi.NewRouter()
	r.Post("/api/v1/auth/logout", h.Logout)
	r.Post("/api/v1/auth/refresh", h.Refresh)
	return r
}

func setCookie(t *testing.T, w *httptest.ResponseRecorder) *http.Cookie {
	t.Helper()
	for _, c := range w.Result().Cookies() {
		if c.Name == "refresh_token" {
			return c
		}
	}
	return nil
}

func post(r http.Handler, path string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(http.MethodPost, path, nil)
	req.AddCookie(&http.Cookie{Name: "refresh_token", Value: "tok"})
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	return w
}

func TestLogout_ExpiresTheRealCookie(t *testing.T) {
	fake := &fakeAuthClient{logout: func(context.Context, *authv1.LogoutRequest, ...grpc.CallOption) (*authv1.LogoutResponse, error) {
		return &authv1.LogoutResponse{}, nil
	}}
	for _, secure := range []bool{false, true} {
		w := post(cookieRouter(fake, secure), "/api/v1/auth/logout")
		if w.Code != http.StatusNoContent {
			t.Fatalf("secure=%v: status %d, want 204", secure, w.Code)
		}
		c := setCookie(t, w)
		if c == nil {
			t.Fatalf("secure=%v: no Set-Cookie for refresh_token", secure)
		}
		// The browser matches name+domain+path, so Path must equal the one the cookie was set with.
		if c.Path != "/api/v1/auth" || c.MaxAge >= 0 || c.Value != "" || !c.HttpOnly || c.Secure != secure {
			t.Errorf("secure=%v: clearing cookie = %+v", secure, c)
		}
	}
}

func TestLogout_BackendFailureIsNotSuccessAndKeepsTheCookie(t *testing.T) {
	fake := &fakeAuthClient{logout: func(context.Context, *authv1.LogoutRequest, ...grpc.CallOption) (*authv1.LogoutResponse, error) {
		return nil, status.Error(codes.Unavailable, "auth down")
	}}
	w := post(cookieRouter(fake, true), "/api/v1/auth/logout")
	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status %d, want 503", w.Code)
	}
	if c := setCookie(t, w); c != nil {
		t.Errorf("cookie cleared despite the failure: %+v (the session is still alive; the user must be able to retry)", c)
	}
}

func TestRefresh_CookieIsSecureWhenConfigured(t *testing.T) {
	fake := &fakeAuthClient{refreshToken: func(context.Context, *authv1.RefreshTokenRequest, ...grpc.CallOption) (*authv1.RefreshTokenResponse, error) {
		return &authv1.RefreshTokenResponse{AccessToken: "a", RefreshToken: "r2"}, nil
	}}
	for _, secure := range []bool{false, true} {
		c := setCookie(t, post(cookieRouter(fake, secure), "/api/v1/auth/refresh"))
		if c == nil || c.Secure != secure || c.Path != "/api/v1/auth" || !c.HttpOnly || c.Value != "r2" {
			t.Errorf("secure=%v: cookie = %+v", secure, c)
		}
	}
}
