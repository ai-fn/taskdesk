// Package auth provides the gateway's local JWT verifier. It moves token
// validation off the per-request gRPC hot path: signatures are checked locally
// with a cached public key (fetched once per signing key via GetJWKS), and the
// user profile (language/plan/email-verified) is served from a short-TTL cache
// backed by GetUserByID. gRPC is touched only on a cache miss.
//
// Bounded staleness: profile changes (e.g. a plan upgrade) propagate within the
// profile cache TTL per pod. This is gateway-only and introduces no security
// regression — auth-service has no access-token revocation today, so an access
// token already lives until its 15-minute expiry regardless of where it's
// verified.
package auth

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"errors"
	"fmt"
	"math"
	"math/big"
	"time"

	"github.com/fsociety/todo/pkg/concurrency"
	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/golang-jwt/jwt/v5"
)

const (
	// keyTTL is long: signing keys are stable (the kid is derived from the key
	// modulus and survives restarts). A rotation simply produces a new kid that
	// misses the cache and triggers one fresh GetJWKS.
	keyTTL = time.Hour
	// profileTTL bounds how stale a user's plan/language may be per pod.
	profileTTL = 30 * time.Second
	// profileSweep is the janitor interval that evicts idle profiles so the
	// per-user cache cannot grow without bound.
	profileSweep = time.Minute
	// grpcTimeout caps each fallback gRPC call made on a cache miss.
	grpcTimeout = 2 * time.Second
)

// ErrInvalidToken marks a token the client must fix (malformed, expired, bad
// signature, unknown signing key). The middleware maps it to 401.
var ErrInvalidToken = errors.New("invalid token")

// UnavailableError marks a failure to reach auth-service on a cache miss. The
// middleware maps it to 503 — the request might succeed once auth recovers.
type UnavailableError struct{ Err error }

func (e *UnavailableError) Error() string { return "auth service unavailable: " + e.Err.Error() }
func (e *UnavailableError) Unwrap() error { return e.Err }

// Profile is the per-user data denormalized from auth-service.
type Profile struct {
	Language      string
	Plan          string
	EmailVerified bool
}

// Identity is the result of a successful verification: token claims + profile.
type Identity struct {
	UserID    string
	Email     string
	SessionID string
	Profile
}

// Verifier verifies access tokens locally using cached signing keys and serves
// user profiles from a short-TTL cache. It is safe for concurrent use.
type Verifier struct {
	client   authv1.AuthServiceClient
	keys     *concurrency.TTLCache[string, *rsa.PublicKey]
	profiles *concurrency.TTLCache[string, Profile]
}

// NewVerifier builds a Verifier backed by the given auth client.
func NewVerifier(client authv1.AuthServiceClient) *Verifier {
	return &Verifier{
		client: client,
		// Key cache: tiny key space, no janitor needed — lazy expiry is enough.
		keys: concurrency.NewTTLCache[string, *rsa.PublicKey](keyTTL),
		// Profile cache: unbounded key space (one per user) — run a janitor.
		profiles: concurrency.NewTTLCache[string, Profile](
			profileTTL,
			concurrency.WithJanitor[string, Profile](profileSweep),
		),
	}
}

// Verify parses and validates tokenStr and returns the caller's identity.
// On the hot path it touches no network: the signing key and profile come from
// caches. Misses fall back to GetJWKS / GetUserByID, each under grpcTimeout, and
// concurrent misses for the same key/user collapse to a single call.
func (v *Verifier) Verify(ctx context.Context, tokenStr string) (*Identity, error) {
	claims := &tokens.Claims{}
	// keyErr captures the classified error from the keyfunc, because
	// jwt.ParseWithClaims rewraps it into an opaque jwt error otherwise.
	var keyErr error
	token, err := jwt.ParseWithClaims(tokenStr, claims, func(t *jwt.Token) (any, error) {
		if _, ok := t.Method.(*jwt.SigningMethodRSA); !ok {
			return nil, fmt.Errorf("%w: unexpected signing method %v", ErrInvalidToken, t.Header["alg"])
		}
		kid, _ := t.Header["kid"].(string)
		if kid == "" {
			return nil, fmt.Errorf("%w: missing kid", ErrInvalidToken)
		}
		pub, err := v.publicKey(ctx, kid)
		if err != nil {
			keyErr = err
			return nil, err
		}
		return pub, nil
	})
	if err != nil {
		if keyErr != nil {
			// Already classified (UnavailableError or ErrInvalidToken-wrapped).
			return nil, keyErr
		}
		// Parse/expiry/signature failure — the client's problem.
		return nil, fmt.Errorf("%w: %v", ErrInvalidToken, err)
	}
	if !token.Valid {
		return nil, fmt.Errorf("%w: claims not valid", ErrInvalidToken)
	}

	profile, err := v.profile(ctx, claims.UserID)
	if err != nil {
		return nil, err // *UnavailableError → 503
	}

	return &Identity{
		UserID:    claims.UserID,
		Email:     claims.Email,
		SessionID: claims.SessionID,
		Profile:   profile,
	}, nil
}

// Invalidate drops a user's cached profile so the next Verify refetches it.
// Exposed for future active-invalidation wiring (see Roadmap).
func (v *Verifier) Invalidate(userID string) { v.profiles.Invalidate(userID) }

// Stop ends the caches' background janitors. Call on server shutdown.
func (v *Verifier) Stop() {
	v.keys.Stop()
	v.profiles.Stop()
}

// publicKey returns the RSA public key for kid, fetching the JWKS on a miss.
func (v *Verifier) publicKey(ctx context.Context, kid string) (*rsa.PublicKey, error) {
	return v.keys.GetOrFetch(ctx, kid, func(ctx context.Context) (*rsa.PublicKey, error) {
		cctx, cancel := context.WithTimeout(ctx, grpcTimeout)
		defer cancel()
		resp, err := v.client.GetJWKS(cctx, &authv1.GetJWKSRequest{})
		if err != nil {
			return nil, &UnavailableError{Err: err}
		}
		for _, jwk := range resp.Keys {
			if jwk.Kid == kid {
				return jwkToRSA(jwk)
			}
		}
		// JWKS reachable but the key is unknown → the token is the problem.
		return nil, fmt.Errorf("%w: unknown signing key %q", ErrInvalidToken, kid)
	})
}

// profile returns the user's profile, fetching it from auth-service on a miss.
func (v *Verifier) profile(ctx context.Context, userID string) (Profile, error) {
	return v.profiles.GetOrFetch(ctx, userID, func(ctx context.Context) (Profile, error) {
		cctx, cancel := context.WithTimeout(ctx, grpcTimeout)
		defer cancel()
		resp, err := v.client.GetUserByID(cctx, &authv1.GetUserByIDRequest{UserId: userID})
		if err != nil {
			return Profile{}, &UnavailableError{Err: err}
		}
		if resp.User == nil {
			return Profile{}, &UnavailableError{Err: errors.New("empty user in response")}
		}
		return Profile{
			Language:      resp.User.Language,
			Plan:          resp.User.Plan,
			EmailVerified: resp.User.EmailVerified,
		}, nil
	})
}

// jwkToRSA reconstructs an RSA public key from a JWK's base64url modulus/exponent.
func jwkToRSA(jwk *authv1.JWK) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(jwk.N)
	if err != nil {
		return nil, &UnavailableError{Err: fmt.Errorf("decode jwk modulus: %w", err)}
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(jwk.E)
	if err != nil {
		return nil, &UnavailableError{Err: fmt.Errorf("decode jwk exponent: %w", err)}
	}
	e := new(big.Int).SetBytes(eBytes)
	if !e.IsInt64() || e.Int64() > math.MaxInt32 {
		return nil, &UnavailableError{Err: errors.New("jwk exponent out of range")}
	}
	return &rsa.PublicKey{
		N: new(big.Int).SetBytes(nBytes),
		E: int(e.Int64()),
	}, nil
}
