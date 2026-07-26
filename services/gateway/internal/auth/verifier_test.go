package auth_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math/big"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// ── test helpers ────────────────────────────────────────────────────────────

func newSigningKey(t *testing.T) (*rsa.PrivateKey, string) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	// Mirror tokens.go kid derivation: first 8 bytes of the modulus, hex.
	kid := fmt.Sprintf("%x", key.N.Bytes()[:8])
	return key, kid
}

func mint(t *testing.T, key *rsa.PrivateKey, kid, userID string, exp time.Time) string {
	t.Helper()
	claims := tokens.Claims{
		UserID:    userID,
		Email:     userID + "@example.com",
		SessionID: "sess-" + userID,
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

func jwkFor(key *rsa.PrivateKey, kid string) *authv1.JWK {
	return &authv1.JWK{
		Kty: "RSA", Kid: kid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(key.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(key.PublicKey.E)).Bytes()),
	}
}

// fakeAuth implements authv1.AuthServiceClient. Only GetJWKS/GetUserByID are
// set; any other call nil-panics through the embedded interface, catching
// accidental use.
type fakeAuth struct {
	authv1.AuthServiceClient
	jwks      func() (*authv1.GetJWKSResponse, error)
	user      func(id string) (*authv1.GetUserByIDResponse, error)
	jwksCalls atomic.Int64
	userCalls atomic.Int64
}

func (f *fakeAuth) GetJWKS(_ context.Context, _ *authv1.GetJWKSRequest, _ ...grpc.CallOption) (*authv1.GetJWKSResponse, error) {
	f.jwksCalls.Add(1)
	return f.jwks()
}

func (f *fakeAuth) GetUserByID(_ context.Context, in *authv1.GetUserByIDRequest, _ ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	f.userCalls.Add(1)
	return f.user(in.UserId)
}

func okProfile(plan, lang string) func(string) (*authv1.GetUserByIDResponse, error) {
	return func(id string) (*authv1.GetUserByIDResponse, error) {
		return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
			UserId: id, Language: lang, Plan: plan, EmailVerified: true,
		}}, nil
	}
}

// ── tests ───────────────────────────────────────────────────────────────────

func TestVerify_ValidToken(t *testing.T) {
	key, kid := newSigningKey(t)
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) {
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(key, kid)}}, nil
		},
		user: okProfile("pro", "zh"),
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	tok := mint(t, key, kid, "u1", time.Now().Add(time.Hour))
	id, err := v.Verify(context.Background(), tok)
	if err != nil {
		t.Fatalf("Verify: %v", err)
	}
	if id.UserID != "u1" || id.Email != "u1@example.com" || id.SessionID != "sess-u1" {
		t.Fatalf("claims wrong: %+v", id)
	}
	if id.Plan != "pro" || id.Language != "zh" || !id.EmailVerified {
		t.Fatalf("profile wrong: %+v", id.Profile)
	}
}

func TestVerify_ConcurrentMissesCollapse(t *testing.T) {
	key, kid := newSigningKey(t)
	release := make(chan struct{})
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) {
			<-release
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(key, kid)}}, nil
		},
		user: func(id string) (*authv1.GetUserByIDResponse, error) {
			<-release
			return okProfile("free", "en")(id)
		},
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	tok := mint(t, key, kid, "u1", time.Now().Add(time.Hour))

	const n = 100
	var wg sync.WaitGroup
	errs := make([]error, n)
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, errs[i] = v.Verify(context.Background(), tok)
		}(i)
	}
	time.Sleep(20 * time.Millisecond) // let callers pile onto the same keys
	close(release)
	wg.Wait()

	for i, err := range errs {
		if err != nil {
			t.Fatalf("caller %d: %v", i, err)
		}
	}
	if got := fake.jwksCalls.Load(); got != 1 {
		t.Fatalf("GetJWKS called %d times, want 1 (singleflight + cache)", got)
	}
	if got := fake.userCalls.Load(); got != 1 {
		t.Fatalf("GetUserByID called %d times, want 1", got)
	}
}

func TestVerify_GarbageToken_NoGRPC(t *testing.T) {
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) { t.Fatal("GetJWKS should not be called"); return nil, nil },
		user: func(string) (*authv1.GetUserByIDResponse, error) {
			t.Fatal("GetUserByID should not be called")
			return nil, nil
		},
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	_, err := v.Verify(context.Background(), "not-a-jwt")
	if err == nil {
		t.Fatal("expected error for garbage token")
	}
	if got := fake.jwksCalls.Load(); got != 0 {
		t.Fatalf("GetJWKS called %d times for unparseable token, want 0", got)
	}
}

func TestVerify_ExpiredToken(t *testing.T) {
	key, kid := newSigningKey(t)
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) {
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(key, kid)}}, nil
		},
		user: okProfile("free", "en"),
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	tok := mint(t, key, kid, "u1", time.Now().Add(-time.Hour)) // expired
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for expired token")
	}
	if got := fake.userCalls.Load(); got != 0 {
		t.Fatalf("GetUserByID called %d times for invalid token, want 0", got)
	}
}

func TestVerify_UnknownKid_Invalid(t *testing.T) {
	key, kid := newSigningKey(t)
	otherKey, _ := newSigningKey(t)
	fake := &fakeAuth{
		// JWKS reachable but advertises a different key than the token's kid.
		jwks: func() (*authv1.GetJWKSResponse, error) {
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(otherKey, "deadbeef")}}, nil
		},
		user: okProfile("free", "en"),
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()
	_ = key

	tok := mint(t, key, kid, "u1", time.Now().Add(time.Hour))
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error for unknown kid")
	}
	var unavail *auth.UnavailableError
	if asUnavailable(err, &unavail) {
		t.Fatalf("unknown kid should not be UnavailableError (got %v)", err)
	}
}

func TestVerify_ProfileCacheHit(t *testing.T) {
	key, kid := newSigningKey(t)
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) {
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(key, kid)}}, nil
		},
		user: okProfile("pro", "en"),
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	tok := mint(t, key, kid, "u1", time.Now().Add(time.Hour))
	for i := 0; i < 3; i++ {
		if _, err := v.Verify(context.Background(), tok); err != nil {
			t.Fatalf("verify %d: %v", i, err)
		}
	}
	if got := fake.userCalls.Load(); got != 1 {
		t.Fatalf("GetUserByID called %d times, want 1 (cache hits)", got)
	}
}

func TestVerify_AuthDownOnMiss_Unavailable(t *testing.T) {
	key, kid := newSigningKey(t)
	fake := &fakeAuth{
		jwks: func() (*authv1.GetJWKSResponse, error) {
			return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{jwkFor(key, kid)}}, nil
		},
		user: func(string) (*authv1.GetUserByIDResponse, error) {
			return nil, status.Error(codes.Unavailable, "auth down")
		},
	}
	v := auth.NewVerifier(fake)
	defer v.Stop()

	tok := mint(t, key, kid, "u1", time.Now().Add(time.Hour))
	_, err := v.Verify(context.Background(), tok)
	if err == nil {
		t.Fatal("expected error when profile fetch fails")
	}
	var unavail *auth.UnavailableError
	if !asUnavailable(err, &unavail) {
		t.Fatalf("want *UnavailableError, got %T: %v", err, err)
	}
}

// asUnavailable is a tiny errors.As wrapper kept separate so the intent reads
// clearly at each call site.
func asUnavailable(err error, target **auth.UnavailableError) bool {
	return errors.As(err, target)
}
