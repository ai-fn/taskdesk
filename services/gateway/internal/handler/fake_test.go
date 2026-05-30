package handler_test

import (
	"context"

	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"google.golang.org/grpc"
)

// fakeAuthClient implements authv1.AuthServiceClient. Only the methods actually
// exercised by handler tests need to have their func fields set; everything
// else panics with a clear message to catch accidental calls.

type fakeAuthClient struct {
	validateToken        func(ctx context.Context, req *authv1.ValidateTokenRequest, opts ...grpc.CallOption) (*authv1.ValidateTokenResponse, error)
	listSessions         func(ctx context.Context, req *authv1.ListSessionsRequest, opts ...grpc.CallOption) (*authv1.ListSessionsResponse, error)
	revokeSession        func(ctx context.Context, req *authv1.RevokeSessionRequest, opts ...grpc.CallOption) (*authv1.RevokeSessionResponse, error)
	revokeOtherSessions  func(ctx context.Context, req *authv1.RevokeOtherSessionsRequest, opts ...grpc.CallOption) (*authv1.RevokeOtherSessionsResponse, error)
	updateUserLanguage   func(ctx context.Context, req *authv1.UpdateUserLanguageRequest, opts ...grpc.CallOption) (*authv1.UpdateUserLanguageResponse, error)
	getUserByID          func(ctx context.Context, req *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error)
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
	panic("not used in handler tests")
}
func (f *fakeAuthClient) ListUserIDs(ctx context.Context, in *authv1.ListUserIDsRequest, opts ...grpc.CallOption) (*authv1.ListUserIDsResponse, error) {
	panic("not used in handler tests")
}
func (f *fakeAuthClient) GetUserByID(ctx context.Context, in *authv1.GetUserByIDRequest, opts ...grpc.CallOption) (*authv1.GetUserByIDResponse, error) {
	if f.getUserByID != nil {
		return f.getUserByID(ctx, in, opts...)
	}
	panic("not used in handler tests")
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
