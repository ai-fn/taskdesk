package handler_test

import (
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
	"google.golang.org/protobuf/types/known/timestamppb"
)

// buildRouter wires the Authenticate middleware + SessionsHandler onto a chi
// router — identical to how production does it in server.go.
func buildRouter(fake *fakeAuthClient) http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.Authenticate(authVerifier(fake)))
	h := handler.NewSessionsHandler(fake)
	r.Get("/api/v1/sessions", h.List)
	r.Delete("/api/v1/sessions/{sessionID}", h.Revoke)
	r.Post("/api/v1/sessions/revoke-others", h.RevokeOthers)
	return r
}

// validToken makes a fake client that authenticates any bearer token as user
// "u1" with session "s1", then delegates actual RPC calls to the provided
// optional func fields.
func validToken(fake *fakeAuthClient) *fakeAuthClient {
	orig := fake.validateToken
	fake.validateToken = func(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
		if orig != nil {
			return orig(ctx, req, opts...)
		}
		return &authv1.ValidateTokenResponse{
			UserId:    "u1",
			Email:     "u1@test.com",
			SessionId: "s1",
		}, nil
	}
	return fake
}

func authedRequest(method, path string) *http.Request {
	r := httptest.NewRequest(method, path, nil)
	r.Header.Set("Authorization", u1Token())
	return r
}

// ── List ──────────────────────────────────────────────────────────────────────

func TestSessionsList_200(t *testing.T) {
	fake := validToken(&fakeAuthClient{
		listSessions: func(_ context.Context, req *authv1.ListSessionsRequest, _ ...grpc.CallOption) (*authv1.ListSessionsResponse, error) {
			if req.UserId != "u1" || req.CurrentSessionId != "s1" {
				t.Errorf("unexpected request: %+v", req)
			}
			return &authv1.ListSessionsResponse{
				Sessions: []*authv1.Session{
					{
						Id:           "sess-uuid",
						Browser:      "Chrome",
						Os:           "Windows",
						Device:       "Desktop",
						IpAddress:    "1.2.3.4",
						Current:      true,
						CreatedAt:    timestamppb.Now(),
						LastActiveAt: timestamppb.Now(),
					},
				},
			}, nil
		},
	})

	w := httptest.NewRecorder()
	buildRouter(fake).ServeHTTP(w, authedRequest("GET", "/api/v1/sessions"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", w.Code, w.Body.String())
	}
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	sessions, ok := body["sessions"].([]any)
	if !ok || len(sessions) != 1 {
		t.Errorf("expected sessions array with 1 element, got %v", body)
	}
}

func TestSessionsList_NoToken_401(t *testing.T) {
	fake := &fakeAuthClient{
		validateToken: func(_ context.Context, _ *authv1.ValidateTokenRequest, _ ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
			return nil, status.Error(codes.Unauthenticated, "bad token")
		},
	}
	w := httptest.NewRecorder()
	buildRouter(fake).ServeHTTP(w, httptest.NewRequest("GET", "/api/v1/sessions", nil))

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

// ── Revoke ────────────────────────────────────────────────────────────────────

func TestSessionsRevoke_204(t *testing.T) {
	fake := validToken(&fakeAuthClient{
		revokeSession: func(_ context.Context, req *authv1.RevokeSessionRequest, _ ...grpc.CallOption) (*authv1.RevokeSessionResponse, error) {
			if req.UserId != "u1" || req.SessionId != "target-sess" {
				t.Errorf("unexpected request: %+v", req)
			}
			return &authv1.RevokeSessionResponse{}, nil
		},
	})

	w := httptest.NewRecorder()
	buildRouter(fake).ServeHTTP(w, authedRequest("DELETE", "/api/v1/sessions/target-sess"))

	if w.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", w.Code)
	}
}

func TestSessionsRevoke_NotFound_404(t *testing.T) {
	fake := validToken(&fakeAuthClient{
		revokeSession: func(_ context.Context, _ *authv1.RevokeSessionRequest, _ ...grpc.CallOption) (*authv1.RevokeSessionResponse, error) {
			return nil, status.Error(codes.NotFound, "session not found")
		},
	})

	w := httptest.NewRecorder()
	buildRouter(fake).ServeHTTP(w, authedRequest("DELETE", "/api/v1/sessions/ghost-sess"))

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want 404", w.Code)
	}
	var body map[string]string
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["error"] != "session not found" {
		t.Errorf(`body["error"] = %q, want "session not found"`, body["error"])
	}
}

// ── RevokeOthers ──────────────────────────────────────────────────────────────

func TestSessionsRevokeOthers_200(t *testing.T) {
	fake := validToken(&fakeAuthClient{
		revokeOtherSessions: func(_ context.Context, req *authv1.RevokeOtherSessionsRequest, _ ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error) {
			if req.UserId != "u1" || req.CurrentSessionId != "s1" {
				t.Errorf("unexpected request: %+v", req)
			}
			return &authv1.RevokeOtherSessionsResponse{Revoked: 3}, nil
		},
	})

	w := httptest.NewRecorder()
	buildRouter(fake).ServeHTTP(w, authedRequest("POST", "/api/v1/sessions/revoke-others"))

	if w.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200: body=%s", w.Code, w.Body.String())
	}
	// protojson serializes int64 as a JSON string.
	var body map[string]any
	if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
		t.Fatalf("body is not JSON: %v", err)
	}
	if body["revoked"] != "3" {
		t.Errorf(`body["revoked"] = %v (%T), want "3"`, body["revoked"], body["revoked"])
	}
}
