package middleware_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// minimalAuthClient implements only ValidateToken; all other methods panic.
type minimalAuthClient struct {
	validateToken func(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
}

func (m *minimalAuthClient) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	return m.validateToken(ctx, req, opts...)
}

func (m *minimalAuthClient) SignUp(ctx context.Context, in *authv1.SignUpRequest, opts ...grpc.CallOption) (*authv1.SignUpResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.LoginResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) RefreshToken(ctx context.Context, in *authv1.RefreshTokenRequest, opts ...grpc.CallOption) (*authv1.RefreshTokenResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) Logout(ctx context.Context, in *authv1.LogoutRequest, opts ...grpc.CallOption) (*authv1.LogoutResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) GetJWKS(ctx context.Context, in *authv1.GetJWKSRequest, opts ...grpc.CallOption) (*authv1.GetJWKSResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) ListUserIDs(ctx context.Context, in *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) GetUserByID(ctx context.Context, in *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) OAuthSignIn(ctx context.Context, in *authv1.OAuthSignInRequest, opts ...grpc.CallOption) (*authv1.OAuthSignInResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) ListSessions(ctx context.Context, in *authv1.ListSessionsRequest, opts ...grpc.CallOption) (*authv1.ListSessionsResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) RevokeSession(ctx context.Context, in *authv1.RevokeSessionRequest, opts ...grpc.CallOption) (*authv1.RevokeSessionResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) RevokeOtherSessions(ctx context.Context, in *authv1.RevokeOtherSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error) {
	panic("not implemented")
}
func (m *minimalAuthClient) UpdateUserLanguage(ctx context.Context, in *authv1.UpdateUserLanguageRequest, opts ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error) {
	panic("not implemented")
}

// probe is a simple next handler that captures the context values set by Authenticate.
type probeResult struct {
	userID, email, sessionID, language string
	called                             bool
}

func probeHandler(result *probeResult) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result.called = true
		result.userID = middleware.UserIDFromContext(r.Context())
		result.email = middleware.EmailFromContext(r.Context())
		result.sessionID = middleware.SessionIDFromContext(r.Context())
		result.language = middleware.LanguageFromContext(r.Context())
		w.WriteHeader(http.StatusOK)
	})
}

func makeAuthMiddleware(fn func(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)) func(http.Handler) http.Handler {
	return middleware.Authenticate(&minimalAuthClient{validateToken: fn})
}

func TestAuthenticate_MissingToken(t *testing.T) {
	mw := makeAuthMiddleware(nil) // ValidateToken should never be called
	probe := &probeResult{}
	handler := mw(probeHandler(probe))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if probe.called {
		t.Error("next handler should not have been called")
	}
}

func TestAuthenticate_ValidToken_PopulatesContext(t *testing.T) {
	mw := makeAuthMiddleware(func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return &authv1.ValidateTokenResponse{
			UserId:    "user123",
			Email:     "test@example.com",
			SessionId: "sess456",
		}, nil
	})
	probe := &probeResult{}
	handler := mw(probeHandler(probe))

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer mytoken")
	handler.ServeHTTP(w, r)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want 200", w.Code)
	}
	if !probe.called {
		t.Fatal("next handler was not called")
	}
	if probe.userID != "user123" {
		t.Errorf("UserID = %q, want user123", probe.userID)
	}
	if probe.email != "test@example.com" {
		t.Errorf("Email = %q, want test@example.com", probe.email)
	}
	if probe.sessionID != "sess456" {
		t.Errorf("SessionID = %q, want sess456", probe.sessionID)
	}
}

func TestAuthenticate_ValidToken_PopulatesLanguage(t *testing.T) {
	mw := makeAuthMiddleware(func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return &authv1.ValidateTokenResponse{UserId: "u", Email: "u@x.com", SessionId: "s", Language: "zh"}, nil
	})
	probe := &probeResult{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer tok")
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if probe.language != "zh" {
		t.Errorf("Language = %q, want %q", probe.language, "zh")
	}
}

func TestAuthenticate_MissingLanguage_DefaultsToEn(t *testing.T) {
	mw := makeAuthMiddleware(func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return &authv1.ValidateTokenResponse{UserId: "u", Email: "u@x.com", SessionId: "s"}, nil
	})
	probe := &probeResult{}
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer tok")
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if probe.language != "en" {
		t.Errorf("Language = %q, want %q", probe.language, "en")
	}
}

func TestAuthenticate_UnauthenticatedError_Returns401(t *testing.T) {
	mw := makeAuthMiddleware(func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return nil, status.Error(codes.Unauthenticated, "token expired")
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer badtoken")
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticate_ServiceError_Returns503(t *testing.T) {
	mw := makeAuthMiddleware(func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		return nil, status.Error(codes.Unavailable, "auth down")
	})
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer sometoken")
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestAuthenticate_QueryParamFallback(t *testing.T) {
	// WebSocket clients can't set headers — they use ?token= instead.
	mw := makeAuthMiddleware(func(_ context.Context, req *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		if req.AccessToken != "wstoken" {
			return nil, status.Error(codes.Unauthenticated, "wrong token")
		}
		return &authv1.ValidateTokenResponse{UserId: "wsuser", Email: "ws@x.com", SessionId: "wssid"}, nil
	})

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/?token=wstoken", nil)
	probe := &probeResult{}
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if w.Code != http.StatusOK || !probe.called {
		t.Errorf("status=%d called=%v, want 200 and called", w.Code, probe.called)
	}
	if probe.userID != "wsuser" {
		t.Errorf("UserID = %q, want wsuser", probe.userID)
	}
}
