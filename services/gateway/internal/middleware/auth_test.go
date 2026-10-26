package middleware_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/auth"
	"github.com/fsociety/todo/services/gateway/internal/middleware"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── token + JWKS test helpers ───────────────────────────────────────────────

func newKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	kid := fmt.Sprintf("%x", key.N.Bytes()[:8])
	return key, kid
}

func mint(t *testing.T, key *rsa.PrivateKey, kid, userID, email, sid string, exp time.Time) string {
	t.Helper()
	return mintTyped(t, key, kid, userID, email, sid, tokens.TypeAccess, exp)
}

func mintTyped(t *testing.T, key *rsa.PrivateKey, kid, userID, email, sid, typ string, exp time.Time) string {
	t.Helper()
	claims := tokens.Claims{
		UserID:    userID,
		Email:     email,
		SessionID: sid,
		Type:      typ,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(exp),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = kid
	s, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// fakeAuth implements authv1.AuthServiceClient; only GetJWKS/GetUserByID are
// wired. Anything else nil-panics through the embedded interface.
type fakeAuth struct {
	authv1.AuthServiceClient
	key  *rsa.PrivateKey
	kid  string
	plan string
	lang string
	// userErr, when set, makes GetUserByID fail (simulates auth-service down).
	userErr error
}

func (f *fakeAuth) GetJWKS(_ context.Context, _ *authv1.GetJWKSRequest, _ ...grpc.CallOption) (*authv1.GetJWKSResponse, error) {
	return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{{
		Kty: "RSA", Kid: f.kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(f.key.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(f.key.PublicKey.E)).Bytes()),
	}}}, nil
}

func (f *fakeAuth) GetUserByID(_ context.Context, in *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	if f.userErr != nil {
		return nil, f.userErr
	}
	return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
		UserId: in.UserId, Language: f.lang, Plan: f.plan, EmailVerified: true,
	}}, nil
}

// newMiddleware builds the Authenticate middleware over a real Verifier backed
// by the fake auth client.
func newMiddleware(t *testing.T, f *fakeAuth) func(http.Handler) http.Handler {
	t.Helper()
	v := auth.NewVerifier(f)
	t.Cleanup(v.Stop)
	return middleware.Authenticate(v)
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

// ── tests ───────────────────────────────────────────────────────────────────

func TestAuthenticate_MissingToken(t *testing.T) {
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free"})
	probe := &probeResult{}

	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
	if probe.called {
		t.Error("next handler should not have been called")
	}
}

func TestAuthenticate_ValidToken_PopulatesContext(t *testing.T) {
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free", lang: "en"})
	probe := &probeResult{}

	tok := mint(t, key, kid, "user123", "test@example.com", "sess456", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	mw(probeHandler(probe)).ServeHTTP(w, r)

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
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free", lang: "zh"})
	probe := &probeResult{}

	tok := mint(t, key, kid, "u", "u@x.com", "s", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if probe.language != "zh" {
		t.Errorf("Language = %q, want %q", probe.language, "zh")
	}
}

func TestAuthenticate_MissingLanguage_DefaultsToEn(t *testing.T) {
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free", lang: ""})
	probe := &probeResult{}

	tok := mint(t, key, kid, "u", "u@x.com", "s", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if probe.language != "en" {
		t.Errorf("Language = %q, want %q", probe.language, "en")
	}
}

func TestAuthenticate_InvalidToken_Returns401(t *testing.T) {
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free"})

	// Expired token: well-formed and correctly signed, but past its exp.
	tok := mint(t, key, kid, "u", "u@x.com", "s", time.Now().Add(-time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, r)

	if w.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", w.Code)
	}
}

func TestAuthenticate_RefreshToken_Returns401(t *testing.T) {
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free"})

	tok := mintTyped(t, key, kid, "u", "u@x.com", "s", tokens.TypeRefresh, time.Now().Add(time.Hour))
	for name, req := range map[string]*http.Request{
		"header": func() *http.Request {
			r := httptest.NewRequest("GET", "/", nil)
			r.Header.Set("Authorization", "Bearer "+tok)
			return r
		}(),
		"query": httptest.NewRequest("GET", "/?token="+tok, nil),
	} {
		w := httptest.NewRecorder()
		mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		if w.Code != http.StatusUnauthorized {
			t.Errorf("%s: status = %d, want 401", name, w.Code)
		}
	}
}

func TestAuthenticate_ServiceError_Returns503(t *testing.T) {
	key, kid := newKey(t)
	// Valid token, but the profile fetch (a cache miss) can't reach auth.
	mw := newMiddleware(t, &fakeAuth{
		key: key, kid: kid,
		userErr: status.Error(codes.Unavailable, "auth down"),
	})

	tok := mint(t, key, kid, "u", "u@x.com", "s", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/", nil)
	r.Header.Set("Authorization", "Bearer "+tok)
	mw(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
	})).ServeHTTP(w, r)

	if w.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503", w.Code)
	}
}

func TestAuthenticate_QueryParamFallback(t *testing.T) {
	// WebSocket clients can't set headers — they use ?token= instead.
	key, kid := newKey(t)
	mw := newMiddleware(t, &fakeAuth{key: key, kid: kid, plan: "free"})
	probe := &probeResult{}

	tok := mint(t, key, kid, "wsuser", "ws@x.com", "wssid", time.Now().Add(time.Hour))
	w := httptest.NewRecorder()
	r := httptest.NewRequest("GET", "/?token="+tok, nil)
	mw(probeHandler(probe)).ServeHTTP(w, r)

	if w.Code != http.StatusOK || !probe.called {
		t.Errorf("status=%d called=%v, want 200 and called", w.Code, probe.called)
	}
	if probe.userID != "wsuser" {
		t.Errorf("UserID = %q, want wsuser", probe.userID)
	}
}
