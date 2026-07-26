package handler_test

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/base64"
	"fmt"
	"math/big"
	"time"

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/gateway/internal/auth"
	"github.com/golang-jwt/jwt/v5"
	"google.golang.org/grpc"
)

// Handler tests authenticate through the real local Verifier, so they need a
// signing key whose public half GetJWKS advertises, plus real minted tokens.
// One key per test binary is enough.
var (
	testKey *rsa.PrivateKey
	testKid string
)

func init() {
	k, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		panic(err)
	}
	testKey = k
	testKid = fmt.Sprintf("%x", k.PublicKey.N.Bytes()[:8]) // mirrors tokens.go
}

// authVerifier builds a Verifier over the fake auth client for use with
// middleware.Authenticate in handler tests.
func authVerifier(fake *fakeAuthClient) *auth.Verifier {
	return auth.NewVerifier(fake)
}

// mintTestToken signs a valid access token for the given identity.
func mintTestToken(userID, email, sid string) string {
	claims := tokens.Claims{
		UserID:    userID,
		Email:     email,
		SessionID: sid,
		RegisteredClaims: jwt.RegisteredClaims{
			Subject:   userID,
			IssuedAt:  jwt.NewNumericDate(time.Now().Add(-time.Minute)),
			ExpiresAt: jwt.NewNumericDate(time.Now().Add(time.Hour)),
		},
	}
	tok := jwt.NewWithClaims(jwt.SigningMethodRS256, claims)
	tok.Header["kid"] = testKid
	s, err := tok.SignedString(testKey)
	if err != nil {
		panic(err)
	}
	return s
}

// u1Token is the bearer header value for the canonical test user (u1/s1).
func u1Token() string { return "Bearer " + mintTestToken("u1", "u1@test.com", "s1") }

// fakeAuthClient implements authv1.AuthServiceClient. Only the methods actually
// exercised by handler tests need to have their func fields set; everything
// else panics with a clear message to catch accidental calls.

type fakeAuthClient struct {
	validateToken       func(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
	listSessions        func(ctx context.Context, req *authv1.ListSessionsRequest, opts ...grpc.CallOption) (*authv1.ListSessionsResponse, error)
	revokeSession       func(ctx context.Context, req *authv1.RevokeSessionRequest, opts ...grpc.CallOption) (*authv1.RevokeSessionResponse, error)
	revokeOtherSessions func(ctx context.Context, req *authv1.RevokeOtherSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error)
	updateUserLanguage  func(ctx context.Context, req *authv1.UpdateUserLanguageRequest, opts ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error)
	getUserByID         func(ctx context.Context, req *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error)
}

func (f *fakeAuthClient) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error) {
	if f.validateToken == nil {
		panic("fakeAuthClient.ValidateToken not configured")
	}
	return f.validateToken(ctx, req, opts...)
}

func (f *fakeAuthClient) ListSessions(ctx context.Context, req *authv1.ListSessionsRequest, opts ...grpc.CallOption) (*authv1.ListSessionsResponse, error) {
	if f.listSessions == nil {
		panic("fakeAuthClient.ListSessions not configured")
	}
	return f.listSessions(ctx, req, opts...)
}

func (f *fakeAuthClient) RevokeSession(ctx context.Context, req *authv1.RevokeSessionRequest, opts ...grpc.CallOption) (*authv1.RevokeSessionResponse, error) {
	if f.revokeSession == nil {
		panic("fakeAuthClient.RevokeSession not configured")
	}
	return f.revokeSession(ctx, req, opts...)
}

func (f *fakeAuthClient) RevokeOtherSessions(ctx context.Context, req *authv1.RevokeOtherSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error) {
	if f.revokeOtherSessions == nil {
		panic("fakeAuthClient.RevokeOtherSessions not configured")
	}
	return f.revokeOtherSessions(ctx, req, opts...)
}

// Remaining interface methods are stubs that panic on call.

func (f *fakeAuthClient) SignUp(ctx context.Context, in *authv1.SignUpRequest, opts ...grpc.CallOption) (*authv1.SignUpResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) Login(ctx context.Context, in *authv1.LoginRequest, opts ...grpc.CallOption) (*authv1.LoginResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) RefreshToken(ctx context.Context, in *authv1.RefreshTokenRequest, opts ...grpc.CallOption) (*authv1.RefreshTokenResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) Logout(ctx context.Context, in *authv1.LogoutRequest, opts ...grpc.CallOption) (*authv1.LogoutResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) GetJWKS(ctx context.Context, in *authv1.GetJWKSRequest, opts ...grpc.CallOption) (*authv1.GetJWKSResponse, error) {
	return &authv1.GetJWKSResponse{Keys: []*authv1.JWK{{
		Kty: "RSA", Kid: testKid, Use: "sig", Alg: "RS256",
		N: base64.RawURLEncoding.EncodeToString(testKey.PublicKey.N.Bytes()),
		E: base64.RawURLEncoding.EncodeToString(big.NewInt(int64(testKey.PublicKey.E)).Bytes()),
	}}}, nil
}
func (f *fakeAuthClient) ListUserIDs(ctx context.Context, in *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) GetUserByID(ctx context.Context, in *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	if f.getUserByID != nil {
		return f.getUserByID(ctx, in, opts...)
	}
	// Default profile so the Verifier can complete authentication in tests that
	// don't care about plan/language. Free plan, English.
	return &authv1.GetUserByIDResponse{User: &authv1.UserSummary{
		UserId: in.UserId, Language: "en", Plan: "free", EmailVerified: true,
	}}, nil
}

func (f *fakeAuthClient) UpdateUserLanguage(ctx context.Context, in *authv1.UpdateUserLanguageRequest, opts ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error) {
	if f.updateUserLanguage != nil {
		return f.updateUserLanguage(ctx, in, opts...)
	}
	panic("fakeAuthClient.UpdateUserLanguage not configured")
}
func (f *fakeAuthClient) OAuthSignIn(ctx context.Context, in *authv1.OAuthSignInRequest, opts ...grpc.CallOption) (*authv1.OAuthSignInResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) RequestPasswordReset(ctx context.Context, in *authv1.RequestPasswordResetRequest, opts ...grpc.CallOption) (*authv1.RequestPasswordResetResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) ResetPassword(ctx context.Context, in *authv1.ResetPasswordRequest, opts ...grpc.CallOption) (*authv1.ResetPasswordResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) RequestEmailVerification(ctx context.Context, in *authv1.RequestEmailVerificationRequest, opts ...grpc.CallOption) (*authv1.RequestEmailVerificationResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) ConfirmEmail(ctx context.Context, in *authv1.ConfirmEmailRequest, opts ...grpc.CallOption) (*authv1.ConfirmEmailResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) RedeemPromoCode(ctx context.Context, in *authv1.RedeemPromoCodeRequest, opts ...grpc.CallOption) (*authv1.RedeemPromoCodeResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) GetSubscription(ctx context.Context, in *authv1.GetSubscriptionRequest, opts ...grpc.CallOption) (*authv1.GetSubscriptionResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) CreateStripeCheckoutSession(ctx context.Context, in *authv1.CreateStripeCheckoutSessionRequest, opts ...grpc.CallOption) (*authv1.CreateStripeCheckoutSessionResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) HandleStripeWebhook(ctx context.Context, in *authv1.HandleStripeWebhookRequest, opts ...grpc.CallOption) (*authv1.HandleStripeWebhookResponse, error) {
	panic("not used in handler tests")
}
