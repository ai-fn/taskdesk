package handler_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/handler"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/go-chi/chi/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

func buildAccountRouter(fake *fakeAuthClient) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(fake))
	h := handler.NewAccountHandler(fake)
	r.Patch("/api/v1/account/language", h.UpdateLanguage)
	r.Get("/api/v1/account/me", h.Me)
	return r
}

func withValidToken(fake *fakeAuthClient) *fakeAuthClient {
	fake.validateToken = func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return &authv1.ValidateTokenResponse{UserId: "u1", Email: "u@test.com", SessionId: "s1", Language: "en"}, nil
	}
	return fake
}

func TestAccountUpdateLanguage(t *testing.T) {
	cases := []struct {
		name       string
		body       string
		grpcErr    error
		wantStatus int
	}{
		{
			name:       "success",
			body:       `{"language":"zh"}`,
			grpcErr:    nil,
			wantStatus: http.StatusNoContent,
		},
		{
			name:       "invalid language rejected by auth service",
			body:       `{"language":"fr"}`,
			grpcErr:    status.Error(codes.InvalidArgument, "unsupported language"),
			wantStatus: http.StatusBadRequest,
		},
		{
			name:       "missing language field",
			body:       `{}`,
			grpcErr:    nil, // not reached; handler returns 400 before calling gRPC
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			fake := withValidToken(&fakeAuthClient{
				updateUserLanguage: func(_ context.Context, req *authv1.UpdateUserLanguageRequest, _ ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error) {
					return &authv1.UpdateUserLanguageResponse{}, tc.grpcErr
				},
			})
			router := buildAccountRouter(fake)
			w := httptest.NewRecorder()
			req := httptest.NewRequest(http.MethodPatch, "/api/v1/account/language", bytes.NewBufferString(tc.body))
			req.Header.Set("Authorization", "Bearer tok")
			req.Header.Set("Content-Type", "application/json")
			router.ServeHTTP(w, req)
			if w.Code != tc.wantStatus {
				t.Errorf("%s: status = %d, want %d (body: %s)", tc.name, w.Code, tc.wantStatus, w.Body.String())
			}
		})
	}
}

func TestAccountMe(t *testing.T) {
	fake := withValidToken(&fakeAuthClient{
		getUserByID: func(_ context.Context, req *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
			return &authv1.GetUserByIDResponse{
				User: &authv1.UserSummary{UserId: req.UserId, Email: "u@test.com", Name: "Test", Language: "zh"},
			}, nil
		},
	})
	router := buildAccountRouter(fake)
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodGet, "/api/v1/account/me", nil)
	req.Header.Set("Authorization", "Bearer tok")
	router.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode response: %v", err)
	}
	if got := body["language"]; got != "zh" {
		t.Errorf("language = %q, want %q", got, "zh")
	}
}
