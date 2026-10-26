// Package server implements the AuthService gRPC server.
package server

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"time"

	"github.com/fsociety/todo/pkg/events"
	"github.com/fsociety/todo/pkg/i18n"
	pkgkafka "github.com/fsociety/todo/pkg/kafka"
	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/password"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/google/uuid"
	ua "github.com/mileusna/useragent"
	stripe "github.com/stripe/stripe-go/v82"
	"github.com/stripe/stripe-go/v82/webhook"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// CodeConfig holds tunable parameters for OTP verification codes.
type CodeConfig struct {
	TTL            time.Duration
	ResendCooldown time.Duration
	MaxAttempts    int
}

// StripeConfig holds credentials and identifiers for the Stripe integration.
type StripeConfig struct {
	SecretKey     string // STRIPE_SECRET_KEY
	WebhookSecret string // STRIPE_WEBHOOK_SECRET — used to verify webhook signatures
	PriceID       string // STRIPE_PRICE_ID — the recurring price to subscribe users to
	SuccessURL    string // STRIPE_SUCCESS_URL — redirect after successful checkout
	CancelURL     string // STRIPE_CANCEL_URL  — redirect when user cancels checkout
}

type Server struct {
	authv1.UnimplementedAuthServiceServer
	store        *store.Store
	tokens       *tokens.Maker
	log          *slog.Logger
	producer     *pkgkafka.Producer // nil if Kafka is not configured
	codeCfg      CodeConfig
	stripe       StripeConfig
	stripeClient *stripe.Client // per-server Stripe client; avoids mutating stripe.Key global
}

func New(st *store.Store, tm *tokens.Maker, log *slog.Logger, producer *pkgkafka.Producer, codeCfg CodeConfig, stripeCfg StripeConfig) *Server {
	return &Server{
		store:        st,
		tokens:       tm,
		log:          log,
		producer:     producer,
		codeCfg:      codeCfg,
		stripe:       stripeCfg,
		stripeClient: stripe.NewClient(stripeCfg.SecretKey),
	}
}

// ── SignUp ────────────────────────────────────────────────────────────────────

func (s *Server) SignUp(ctx context.Context, req *authv1.SignUpRequest) (*authv1.SignUpResponse, error) {
	if req.Email == "" || req.Password == "" || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "email, password and name are required")
	}
	if err := password.Validate(req.Password); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, status.Error(codes.Internal, "password hashing failed")
	}
	hashStr := string(hash)

	now := time.Now().UTC()
	user := &domain.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: &hashStr,
		Language:     "en",
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := s.store.CreateUser(ctx, user); err != nil {
		if errors.Is(err, store.ErrEmailTaken) {
			return nil, status.Error(codes.AlreadyExists, "email already registered")
		}
		s.log.Error("create user", "err", err)
		return nil, status.Error(codes.Internal, "create user failed")
	}

	sessionID := uuid.New().String()
	access, refresh, err := s.mintTokens(user.ID, user.Email, sessionID)
	if err != nil {
		return nil, err
	}

	if err := s.createLoginSession(ctx, sessionID, user.ID, refresh, req.ClientInfo, hashStr); err != nil {
		return nil, err
	}

	return &authv1.SignUpResponse{
		UserId:       user.ID,
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// ── Login ─────────────────────────────────────────────────────────────────────

func (s *Server) Login(ctx context.Context, req *authv1.LoginRequest) (*authv1.LoginResponse, error) {
	if req.Email == "" || req.Password == "" {
		return nil, status.Error(codes.InvalidArgument, "email and password are required")
	}

	user, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "invalid credentials")
		}
		s.log.Error("get user by email", "err", err)
		return nil, status.Error(codes.Internal, "login failed")
	}

	if user.PasswordHash == nil {
		// OAuth-only account — no password to compare against.
		return nil, status.Error(codes.Unauthenticated, "no password set for this account; use OAuth to sign in")
	}
	if err := bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.Password)); err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid credentials")
	}

	sessionID := uuid.New().String()
	access, refresh, err := s.mintTokens(user.ID, user.Email, sessionID)
	if err != nil {
		return nil, err
	}

	if err := s.createLoginSession(ctx, sessionID, user.ID, refresh, req.ClientInfo, *user.PasswordHash); err != nil {
		return nil, err
	}

	return &authv1.LoginResponse{
		UserId:       user.ID,
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// ── RefreshToken ──────────────────────────────────────────────────────────────

func (s *Server) RefreshToken(ctx context.Context, req *authv1.RefreshTokenRequest) (*authv1.RefreshTokenResponse, error) {
	if req.RefreshToken == "" {
		return nil, status.Error(codes.InvalidArgument, "refresh_token is required")
	}

	// Verify JWT signature + expiry first so we don't hit the DB with garbage.
	claims, err := s.tokens.Verify(req.RefreshToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid refresh token")
	}

	// Mint new tokens carrying the same session ID.
	newExpiry := time.Now().UTC().Add(tokens.RefreshTokenTTL)
	access, refresh, err := s.mintTokens(claims.UserID, claims.Email, claims.SessionID)
	if err != nil {
		return nil, err
	}

	// Rotate in-place: updates token_hash + last_active_at; id stays stable.
	_, _, err = s.store.RotateSession(ctx, hashToken(req.RefreshToken), hashToken(refresh), newExpiry)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "refresh token revoked or expired")
		}
		s.log.Error("rotate session", "err", err)
		return nil, status.Error(codes.Internal, "refresh failed")
	}

	return &authv1.RefreshTokenResponse{
		AccessToken:  access,
		RefreshToken: refresh,
	}, nil
}

// ── Logout ────────────────────────────────────────────────────────────────────

func (s *Server) Logout(ctx context.Context, req *authv1.LogoutRequest) (*authv1.LogoutResponse, error) {
	if req.RefreshToken == "" {
		return &authv1.LogoutResponse{}, nil
	}
	_ = s.store.DeleteSessionByTokenHash(ctx, hashToken(req.RefreshToken))
	return &authv1.LogoutResponse{}, nil
}

// ── GetJWKS ───────────────────────────────────────────────────────────────────

func (s *Server) GetJWKS(_ context.Context, _ *authv1.GetJWKSRequest) (*authv1.GetJWKSResponse, error) {
	return &authv1.GetJWKSResponse{
		Keys: []*authv1.JWK{
			{
				Kty: "RSA",
				Kid: s.tokens.KID(),
				Use: "sig",
				Alg: "RS256",
				N:   s.tokens.PublicKeyN(),
				E:   s.tokens.PublicKeyE(),
			},
		},
	}, nil
}

// ── ValidateToken ─────────────────────────────────────────────────────────────

func (s *Server) ValidateToken(ctx context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	if req.AccessToken == "" {
		return nil, status.Error(codes.InvalidArgument, "access_token is required")
	}
	claims, err := s.tokens.VerifyAccess(req.AccessToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, fmt.Sprintf("invalid token: %v", err))
	}
	user, err := s.store.GetUserByID(ctx, claims.UserID)
	if err != nil {
		s.log.Warn("ValidateToken: get user failed, falling back to language-only", "user_id", claims.UserID, "err", err)
		lang, _ := s.store.GetUserLanguage(ctx, claims.UserID)
		return &authv1.ValidateTokenResponse{
			UserId:           claims.UserID,
			Email:            claims.Email,
			SessionId:        claims.SessionID,
			Language:         lang,
			SubscriptionPlan: "free",
		}, nil
	}
	return &authv1.ValidateTokenResponse{
		UserId:           claims.UserID,
		Email:            claims.Email,
		SessionId:        claims.SessionID,
		Language:         user.Language,
		EmailVerified:    user.EmailVerified,
		SubscriptionPlan: user.Plan,
	}, nil
}

// linkExisting links the OAuth identity to an existing account, claiming it first if unverified.
func (s *Server) linkExisting(ctx context.Context, existing *domain.User, req *authv1.OAuthSignInRequest) error {
	if err := s.claimIfUnverified(ctx, existing, req.Provider); err != nil {
		return err
	}
	return s.store.LinkOAuth(ctx, existing.ID, req.Provider, req.ProviderUserId, req.Email)
}

// claimIfUnverified: the provider proved email ownership, so an unverified (possibly
// attacker pre-registered) account is taken over instead of shared.
func (s *Server) claimIfUnverified(ctx context.Context, u *domain.User, provider string) error {
	if u.EmailVerified {
		return nil
	}
	claimed, err := s.store.ClaimUnverifiedAccount(ctx, u.ID)
	if err != nil {
		return err
	}
	if claimed {
		s.log.Warn("oauth claimed unverified account", "user_id", u.ID, "provider", provider)
	}
	return nil
}

// ── ValidateSession ───────────────────────────────────────────────────────────

func (s *Server) ValidateSession(ctx context.Context, req *authv1.ValidateSessionRequest) (*authv1.ValidateSessionResponse, error) {
	if req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "session_id is required")
	}
	// A non-UUID id can't match a row; querying it would fail the uuid cast instead.
	sid, err := uuid.Parse(req.SessionId)
	if err != nil {
		return &authv1.ValidateSessionResponse{Active: false}, nil
	}
	active, err := s.store.SessionActive(ctx, sid.String())
	if err != nil {
		s.log.Error("validate session", "err", err)
		return nil, status.Error(codes.Internal, "validate session failed")
	}
	return &authv1.ValidateSessionResponse{Active: active}, nil
}

// ── ListUserIDs ───────────────────────────────────────────────────────────────

func (s *Server) ListUserIDs(ctx context.Context, req *authv1.ListUserIDsRequest) (*authv1.ListUserIDsResponse, error) {
	users, err := s.store.ListUsers(ctx, req.PlanFilter)
	if err != nil {
		s.log.Error("list users", "err", err)
		return nil, status.Error(codes.Internal, "list users failed")
	}
	resp := &authv1.ListUserIDsResponse{}
	for _, u := range users {
		resp.Users = append(resp.Users, &authv1.UserSummary{
			UserId:        u.ID,
			Email:         u.Email,
			Name:          u.Name,
			Language:      u.Language,
			EmailVerified: u.EmailVerified,
			Plan:          u.Plan,
		})
	}
	return resp, nil
}

// ── GetUserByID ───────────────────────────────────────────────────────────────

func (s *Server) GetUserByID(ctx context.Context, req *authv1.GetUserByIDRequest) (*authv1.GetUserByIDResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}
	u, err := s.store.GetUserByID(ctx, req.UserId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		s.log.Error("get user by id", "err", err)
		return nil, status.Error(codes.Internal, "get user failed")
	}
	return &authv1.GetUserByIDResponse{
		User: &authv1.UserSummary{
			UserId:        u.ID,
			Email:         u.Email,
			Name:          u.Name,
			Language:      u.Language,
			EmailVerified: u.EmailVerified,
			Plan:          u.Plan,
		},
	}, nil
}

// ── OAuthSignIn ───────────────────────────────────────────────────────────────

// OAuthSignIn handles a verified OAuth identity from the gateway.
// Decision tree:
//  1. Identity already linked → return existing user, created=false.
//  2. Email matches existing user → link identity, created=false. An account with an
//     unverified email is claimed first (see linkExisting).
//  3. Otherwise → create user (no password) + link identity, created=true.
func (s *Server) OAuthSignIn(ctx context.Context, req *authv1.OAuthSignInRequest) (*authv1.OAuthSignInResponse, error) {
	if req.Provider == "" || req.ProviderUserId == "" || req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "provider, provider_user_id, email are required")
	}

	created := false
	var user *domain.User

	// 1. Existing OAuth link?
	u, err := s.store.FindByOAuth(ctx, req.Provider, req.ProviderUserId)
	if err == nil {
		// Also heals accounts linked before unverified ones were claimed.
		if err := s.claimIfUnverified(ctx, u, req.Provider); err != nil {
			s.log.Error("claim unverified (oauth link)", "err", err)
			return nil, status.Error(codes.Internal, "oauth signin failed")
		}
		user = u
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("find by oauth", "err", err)
		return nil, status.Error(codes.Internal, "oauth signin failed")
	}

	// 2. Match by email?
	if user == nil {
		existing, err := s.store.GetUserByEmail(ctx, req.Email)
		if err == nil {
			if err := s.linkExisting(ctx, existing, req); err != nil {
				s.log.Error("link oauth (existing user)", "err", err)
				return nil, status.Error(codes.Internal, "oauth link failed")
			}
			user = existing
		} else if !errors.Is(err, store.ErrNotFound) {
			s.log.Error("get user by email (oauth)", "err", err)
			return nil, status.Error(codes.Internal, "oauth signin failed")
		}
	}

	// 3. Fresh user.
	if user == nil {
		now := time.Now().UTC()
		newUser := &domain.User{
			ID:        uuid.New().String(),
			Email:     req.Email,
			Name:      req.Name,
			Language:  "en",
			CreatedAt: now,
			UpdatedAt: now,
		}
		if err := s.store.CreateUserNoPassword(ctx, newUser); err != nil {
			// Email may have been taken between our check and insert — retry the link path.
			if errors.Is(err, store.ErrEmailTaken) {
				existing, gerr := s.store.GetUserByEmail(ctx, req.Email)
				if gerr != nil {
					s.log.Error("get user after race", "err", gerr)
					return nil, status.Error(codes.Internal, "oauth signin failed")
				}
				if err := s.linkExisting(ctx, existing, req); err != nil {
					s.log.Error("link oauth (race resolution)", "err", err)
					return nil, status.Error(codes.Internal, "oauth link failed")
				}
				user = existing
			} else {
				s.log.Error("create oauth user", "err", err)
				return nil, status.Error(codes.Internal, "oauth signin failed")
			}
		} else {
			if err := s.store.LinkOAuth(ctx, newUser.ID, req.Provider, req.ProviderUserId, req.Email); err != nil {
				s.log.Error("link oauth (new user)", "err", err)
				return nil, status.Error(codes.Internal, "oauth link failed")
			}
			user = newUser
			created = true
		}
	}

	sessionID := uuid.New().String()
	access, refresh, err := s.mintTokens(user.ID, user.Email, sessionID)
	if err != nil {
		return nil, err
	}
	if err := s.createSession(ctx, sessionID, user.ID, refresh, req.ClientInfo); err != nil {
		return nil, err
	}

	s.log.Info("oauth signin",
		"provider", req.Provider,
		"user_id", user.ID,
		"created", created,
	)
	return &authv1.OAuthSignInResponse{
		UserId:       user.ID,
		AccessToken:  access,
		RefreshToken: refresh,
		Created:      created,
	}, nil
}

// ── UpdateUserLanguage ────────────────────────────────────────────────────────

func (s *Server) UpdateUserLanguage(ctx context.Context, req *authv1.UpdateUserLanguageRequest) (*authv1.UpdateUserLanguageResponse, error) {
	if req.UserId == "" || req.Language == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and language are required")
	}
	if !i18n.Supported(req.Language) {
		return nil, status.Error(codes.InvalidArgument, "unsupported language")
	}
	lang := i18n.Normalize(req.Language)
	if err := s.store.UpdateUserLanguage(ctx, req.UserId, lang); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		s.log.Error("update user language", "err", err)
		return nil, status.Error(codes.Internal, "update language failed")
	}
	return &authv1.UpdateUserLanguageResponse{}, nil
}

// ── ListSessions ──────────────────────────────────────────────────────────────

func (s *Server) ListSessions(ctx context.Context, req *authv1.ListSessionsRequest) (*authv1.ListSessionsResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	sessions, err := s.store.ListSessions(ctx, req.UserId)
	if err != nil {
		s.log.Error("list sessions", "err", err)
		return nil, status.Error(codes.Internal, "list sessions failed")
	}

	resp := &authv1.ListSessionsResponse{}
	for _, sess := range sessions {
		parsed := ua.Parse(sess.UserAgent)
		resp.Sessions = append(resp.Sessions, &authv1.Session{
			Id:           sess.ID,
			Browser:      parsed.Name,
			Os:           parsed.OS,
			Device:       deviceLabel(parsed),
			IpAddress:    sess.IPAddress,
			CreatedAt:    timestamppb.New(sess.CreatedAt),
			LastActiveAt: timestamppb.New(sess.LastActiveAt),
			Current:      sess.ID == req.CurrentSessionId,
		})
	}
	return resp, nil
}

// ── RevokeSession ─────────────────────────────────────────────────────────────

func (s *Server) RevokeSession(ctx context.Context, req *authv1.RevokeSessionRequest) (*authv1.RevokeSessionResponse, error) {
	if req.UserId == "" || req.SessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and session_id are required")
	}

	if err := s.store.RevokeSession(ctx, req.UserId, req.SessionId); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "session not found")
		}
		s.log.Error("revoke session", "err", err)
		return nil, status.Error(codes.Internal, "revoke session failed")
	}
	return &authv1.RevokeSessionResponse{}, nil
}

// ── RevokeOtherSessions ───────────────────────────────────────────────────────

func (s *Server) RevokeOtherSessions(ctx context.Context, req *authv1.RevokeOtherSessionsRequest) (*authv1.RevokeOtherSessionsResponse, error) {
	if req.UserId == "" || req.CurrentSessionId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and current_session_id are required")
	}

	n, err := s.store.RevokeOtherSessions(ctx, req.UserId, req.CurrentSessionId)
	if err != nil {
		s.log.Error("revoke other sessions", "err", err)
		return nil, status.Error(codes.Internal, "revoke sessions failed")
	}
	return &authv1.RevokeOtherSessionsResponse{Revoked: n}, nil
}

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *Server) mintTokens(userID, email, sessionID string) (access, refresh string, err error) {
	access, err = s.tokens.CreateAccessToken(userID, email, sessionID)
	if err != nil {
		s.log.Error("create access token", "err", err)
		return "", "", status.Error(codes.Internal, "token creation failed")
	}
	refresh, err = s.tokens.CreateRefreshToken(userID, email, sessionID)
	if err != nil {
		s.log.Error("create refresh token", "err", err)
		return "", "", status.Error(codes.Internal, "token creation failed")
	}
	return access, refresh, nil
}

func (s *Server) createSession(ctx context.Context, sessionID, userID, refreshToken string, info *authv1.ClientInfo) error {
	ip, agent, expiresAt := sessionMeta(info)
	if err := s.store.CreateSession(ctx, sessionID, userID, hashToken(refreshToken), ip, agent, expiresAt); err != nil {
		s.log.Error("create session", "err", err)
		return status.Error(codes.Internal, "save session failed")
	}
	return nil
}

// createLoginSession is createSession for password logins: it fails Unauthenticated if the
// password changed or was cleared (e.g. by an OAuth account claim) since it was verified.
func (s *Server) createLoginSession(ctx context.Context, sessionID, userID, refreshToken string, info *authv1.ClientInfo, passwordHash string) error {
	ip, agent, expiresAt := sessionMeta(info)
	err := s.store.CreateLoginSession(ctx, sessionID, userID, hashToken(refreshToken), ip, agent, expiresAt, passwordHash)
	if errors.Is(err, store.ErrNotFound) {
		return status.Error(codes.Unauthenticated, "invalid credentials")
	}
	if err != nil {
		s.log.Error("create session", "err", err)
		return status.Error(codes.Internal, "save session failed")
	}
	return nil
}

func sessionMeta(info *authv1.ClientInfo) (ip, agent string, expiresAt time.Time) {
	if info != nil {
		ip, agent = info.IpAddress, info.UserAgent
	}
	return ip, agent, time.Now().UTC().Add(tokens.RefreshTokenTTL)
}

// deviceLabel returns a short human-readable label for the UA's device type.
func deviceLabel(parsed ua.UserAgent) string {
	if parsed.Mobile {
		return "Mobile"
	}
	if parsed.Tablet {
		return "Tablet"
	}
	if parsed.Desktop {
		return "Desktop"
	}
	if parsed.Bot {
		return "Bot"
	}
	return "Unknown"
}

// ── RequestPasswordReset ──────────────────────────────────────────────────────

// RequestPasswordReset always returns OK to avoid user enumeration via email.
func (s *Server) RequestPasswordReset(ctx context.Context, req *authv1.RequestPasswordResetRequest) (*authv1.RequestPasswordResetResponse, error) {
	if req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "email is required")
	}

	user, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		// User not found — return OK silently.
		return &authv1.RequestPasswordResetResponse{}, nil
	}

	if err := s.issueCode(ctx, user, events.PurposePasswordReset); err != nil {
		return nil, err
	}
	return &authv1.RequestPasswordResetResponse{}, nil
}

// ── ResetPassword ─────────────────────────────────────────────────────────────

func (s *Server) ResetPassword(ctx context.Context, req *authv1.ResetPasswordRequest) (*authv1.ResetPasswordResponse, error) {
	if req.Email == "" || req.Code == "" || req.NewPassword == "" {
		return nil, status.Error(codes.InvalidArgument, "email, code and new_password are required")
	}
	if err := password.Validate(req.NewPassword); err != nil {
		return nil, status.Error(codes.InvalidArgument, err.Error())
	}

	user, err := s.store.GetUserByEmail(ctx, req.Email)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, "invalid or expired code")
	}

	// The code is checked first so only its holder can learn whether a password is the
	// current one; a rejected reuse leaves the code unconsumed for a retry.
	vc, err := s.verifyCode(ctx, user.ID, events.PurposePasswordReset, req.Code)
	if err != nil {
		return nil, err
	}
	if user.PasswordHash != nil &&
		bcrypt.CompareHashAndPassword([]byte(*user.PasswordHash), []byte(req.NewPassword)) == nil {
		return nil, status.Error(codes.InvalidArgument, "new password must be different from your current password")
	}
	if err := s.consumeCode(ctx, vc.ID); err != nil {
		return nil, err
	}

	hash, err := bcrypt.GenerateFromPassword([]byte(req.NewPassword), bcrypt.DefaultCost)
	if err != nil {
		s.log.Error("hash password", "err", err)
		return nil, status.Error(codes.Internal, "password reset failed")
	}
	if err := s.store.UpdatePassword(ctx, user.ID, string(hash)); err != nil {
		s.log.Error("update password", "err", err)
		return nil, status.Error(codes.Internal, "password reset failed")
	}

	// Revoke all sessions — forces fresh login after reset.
	if _, err := s.store.RevokeAllSessions(ctx, user.ID); err != nil {
		s.log.Error("revoke all sessions after reset", "user_id", user.ID, "err", err)
	}
	return &authv1.ResetPasswordResponse{}, nil
}

// ── RequestEmailVerification ──────────────────────────────────────────────────

func (s *Server) RequestEmailVerification(ctx context.Context, req *authv1.RequestEmailVerificationRequest) (*authv1.RequestEmailVerificationResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	user, err := s.store.GetUserByID(ctx, req.UserId)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "user not found")
		}
		s.log.Error("get user for email verify", "err", err)
		return nil, status.Error(codes.Internal, "request failed")
	}

	if user.EmailVerified {
		return nil, status.Error(codes.AlreadyExists, "email already verified")
	}

	if err := s.issueCode(ctx, user, events.PurposeEmailVerify); err != nil {
		return nil, err
	}
	return &authv1.RequestEmailVerificationResponse{}, nil
}

// ── ConfirmEmail ──────────────────────────────────────────────────────────────

func (s *Server) ConfirmEmail(ctx context.Context, req *authv1.ConfirmEmailRequest) (*authv1.ConfirmEmailResponse, error) {
	if req.UserId == "" || req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and code are required")
	}

	if err := s.verifyAndConsumeCode(ctx, req.UserId, events.PurposeEmailVerify, req.Code); err != nil {
		return nil, err
	}

	if err := s.store.SetEmailVerified(ctx, req.UserId); err != nil {
		s.log.Error("set email verified", "user_id", req.UserId, "err", err)
		return nil, status.Error(codes.Internal, "confirm email failed")
	}
	return &authv1.ConfirmEmailResponse{}, nil
}

// ── RedeemPromoCode ───────────────────────────────────────────────────────────

func (s *Server) RedeemPromoCode(ctx context.Context, req *authv1.RedeemPromoCodeRequest) (*authv1.RedeemPromoCodeResponse, error) {
	if req.UserId == "" || req.Email == "" || req.Code == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id, email and code are required")
	}

	pc, err := s.store.GetPromoCode(ctx, req.Code)
	if err != nil {
		if errors.Is(err, store.ErrPromoCodeNotFound) {
			return nil, status.Error(codes.NotFound, "promo code not found")
		}
		s.log.Error("get promo code", "err", err)
		return nil, status.Error(codes.Internal, "redeem failed")
	}

	if pc.Disabled {
		return nil, status.Error(codes.FailedPrecondition, "promo code is disabled")
	}

	subID := uuid.New().String()
	if err := s.store.RedeemPromoCode(ctx, pc, req.UserId, req.Email, subID); err != nil {
		switch {
		case errors.Is(err, store.ErrUserHasActiveStripeSubscription):
			return nil, status.Error(codes.FailedPrecondition, "cancel your active paid subscription before redeeming a promo code")
		case errors.Is(err, store.ErrPromoCodeDisabled):
			return nil, status.Error(codes.FailedPrecondition, "promo code is disabled")
		case errors.Is(err, store.ErrPromoCodeEmailMismatch):
			return nil, status.Error(codes.PermissionDenied, "promo code is not valid for your account")
		case errors.Is(err, store.ErrPromoCodeExhausted):
			return nil, status.Error(codes.ResourceExhausted, "promo code has reached its usage limit")
		case errors.Is(err, store.ErrPromoCodePerUserLimitReached):
			return nil, status.Error(codes.AlreadyExists, "you have already used this promo code")
		default:
			s.log.Error("redeem promo code", "err", err)
			return nil, status.Error(codes.Internal, "redeem failed")
		}
	}

	s.log.Info("promo code redeemed", "user_id", req.UserId, "code", pc.Code)
	return &authv1.RedeemPromoCodeResponse{Plan: "pro"}, nil
}

// ── GetSubscription ───────────────────────────────────────────────────────────

func (s *Server) GetSubscription(ctx context.Context, req *authv1.GetSubscriptionRequest) (*authv1.GetSubscriptionResponse, error) {
	if req.UserId == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id is required")
	}

	subs, err := s.store.GetActiveSubscriptions(ctx, req.UserId)
	if err != nil {
		s.log.Error("get active subscriptions", "err", err)
		return nil, status.Error(codes.Internal, "get subscription failed")
	}

	info := &authv1.SubscriptionInfo{Plan: "free"}
	if len(subs) > 0 {
		info.Plan = "pro"
		info.Source = subs[0].Source
		info.IsActive = true
	}
	return &authv1.GetSubscriptionResponse{Subscription: info}, nil
}

// ── CreateStripeCheckoutSession ───────────────────────────────────────────────

func (s *Server) CreateStripeCheckoutSession(ctx context.Context, req *authv1.CreateStripeCheckoutSessionRequest) (*authv1.CreateStripeCheckoutSessionResponse, error) {
	if req.UserId == "" || req.Email == "" {
		return nil, status.Error(codes.InvalidArgument, "user_id and email are required")
	}
	// Guard: check for an existing Stripe subscription before touching the Stripe
	// API. Placed before the SecretKey guard so the caller gets a meaningful
	// AlreadyExists error even when Stripe is mis-configured, and so this logic
	// can be exercised in integration tests without a live Stripe key.
	// Promo-code subscriptions do NOT block a user from upgrading via Stripe.
	existingSubs, err := s.store.GetActiveSubscriptions(ctx, req.UserId)
	if err != nil {
		s.log.Error("get active subscriptions for checkout guard", "err", err)
		return nil, status.Error(codes.Internal, "checkout failed")
	}
	for _, sub := range existingSubs {
		if sub.Source == "stripe" {
			return nil, status.Error(codes.AlreadyExists, "you already have an active Stripe subscription")
		}
	}

	if s.stripe.SecretKey == "" {
		return nil, status.Error(codes.Unimplemented, "stripe not configured")
	}

	user, err := s.store.GetUserByID(ctx, req.UserId)
	if err != nil {
		s.log.Error("get user for checkout", "err", err)
		return nil, status.Error(codes.Internal, "checkout failed")
	}

	// Ensure the user has a Stripe customer record.
	// Each Stripe API call gets its own 10 s timeout so a slow customer.New
	// cannot consume the entire budget and starve session.New.
	customerID := ""
	if user.StripeCustomerID != nil {
		customerID = *user.StripeCustomerID
	} else {
		customerCtx, customerCancel := context.WithTimeout(ctx, 10*time.Second)
		defer customerCancel()
		c, err := s.stripeClient.V1Customers.Create(customerCtx, &stripe.CustomerCreateParams{
			Email:    stripe.String(req.Email),
			Metadata: map[string]string{"user_id": req.UserId},
		})
		if err != nil {
			s.log.Error("create stripe customer", "err", err)
			return nil, status.Error(codes.Internal, "checkout failed")
		}
		customerID = c.ID
		// A parallel checkout may have won; use its customer so the user keeps one.
		stored, err := s.store.SetStripeCustomerIDIfUnset(ctx, req.UserId, customerID)
		if err != nil {
			// Non-fatal: the webhook resolves the user from client_reference_id.
			s.log.Error("store stripe customer id", "err", err)
		} else {
			customerID = stored
		}
	}

	sessionCtx, sessionCancel := context.WithTimeout(ctx, 10*time.Second)
	defer sessionCancel()
	params := &stripe.CheckoutSessionCreateParams{
		Customer: stripe.String(customerID),
		Mode:     stripe.String(string(stripe.CheckoutSessionModeSubscription)),
		LineItems: []*stripe.CheckoutSessionCreateLineItemParams{
			{Price: stripe.String(s.stripe.PriceID), Quantity: stripe.Int64(1)},
		},
		SuccessURL: stripe.String(s.stripe.SuccessURL),
		CancelURL:  stripe.String(s.stripe.CancelURL),
		// The webhook finds the user from these even if the customer id was never stored.
		ClientReferenceID: stripe.String(req.UserId),
		Metadata:          map[string]string{"user_id": req.UserId},
		SubscriptionData:  &stripe.CheckoutSessionCreateSubscriptionDataParams{Metadata: map[string]string{"user_id": req.UserId}},
	}
	sess, err := s.stripeClient.V1CheckoutSessions.Create(sessionCtx, params)
	if err != nil {
		s.log.Error("create stripe checkout session", "err", err)
		return nil, status.Error(codes.Internal, "checkout failed")
	}

	return &authv1.CreateStripeCheckoutSessionResponse{CheckoutUrl: sess.URL}, nil
}

// ── HandleStripeWebhook ───────────────────────────────────────────────────────

func (s *Server) HandleStripeWebhook(ctx context.Context, req *authv1.HandleStripeWebhookRequest) (*authv1.HandleStripeWebhookResponse, error) {
	if s.stripe.WebhookSecret == "" {
		return nil, status.Error(codes.Unimplemented, "stripe not configured")
	}

	event, err := webhook.ConstructEventWithOptions(req.Payload, req.Signature, s.stripe.WebhookSecret,
		webhook.ConstructEventOptions{IgnoreAPIVersionMismatch: true})
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid stripe webhook signature")
	}

	switch event.Type {
	case "checkout.session.completed", "checkout.session.async_payment_succeeded":
		if err := s.handleCheckoutCompleted(ctx, &event); err != nil {
			s.log.Error("handle checkout session", "type", event.Type, "err", err)
			return nil, status.Error(codes.Internal, "webhook processing failed")
		}
	case "customer.subscription.updated":
		if err := s.handleSubscriptionUpdated(ctx, &event); err != nil {
			s.log.Error("handle customer.subscription.updated", "err", err)
			return nil, status.Error(codes.Internal, "webhook processing failed")
		}
	case "customer.subscription.deleted":
		if err := s.handleSubscriptionDeleted(ctx, &event); err != nil {
			s.log.Error("handle customer.subscription.deleted", "err", err)
			return nil, status.Error(codes.Internal, "webhook processing failed")
		}
	default:
		// Unhandled event types are not errors — Stripe sends many event types.
		s.log.Info("unhandled stripe event", "type", event.Type)
	}

	return &authv1.HandleStripeWebhookResponse{}, nil
}

func (s *Server) handleCheckoutCompleted(ctx context.Context, event *stripe.Event) error {
	var sess stripe.CheckoutSession
	if err := json.Unmarshal(event.Data.Raw, &sess); err != nil {
		return fmt.Errorf("unmarshal checkout session: %w", err)
	}
	if sess.Subscription == nil || sess.Customer == nil {
		return nil
	}
	// Async methods complete the session before the money arrives; async_payment_succeeded follows.
	if sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusPaid &&
		sess.PaymentStatus != stripe.CheckoutSessionPaymentStatusNoPaymentRequired {
		s.log.Info("stripe checkout not paid yet, waiting", "session_id", sess.ID, "payment_status", sess.PaymentStatus)
		return nil
	}

	user, err := s.stripeCheckoutUser(ctx, &sess)
	if err != nil {
		return err
	}

	sub := &domain.Subscription{
		ID:        uuid.New().String(),
		UserID:    user.ID,
		Plan:      "pro",
		Source:    "stripe",
		SourceRef: sess.Subscription.ID,
		Status:    "active",
		StartedAt: time.Now().UTC(),
	}
	if err := s.store.CreateSubscriptionAndSetPlan(ctx, sub, "pro"); err != nil {
		if errors.Is(err, store.ErrSubscriptionAlreadyExists) {
			s.log.Info("stripe checkout already processed (db constraint)", "subscription_id", sess.Subscription.ID)
			return nil
		}
		return fmt.Errorf("create subscription and set plan: %w", err)
	}
	s.log.Info("stripe subscription activated", "user_id", user.ID, "subscription_id", sess.Subscription.ID)
	return nil
}

// stripeCheckoutUser resolves the paying user from the id we put on the session, falling back
// to the customer id for sessions created before that. It backfills a missing customer id.
func (s *Server) stripeCheckoutUser(ctx context.Context, sess *stripe.CheckoutSession) (*domain.User, error) {
	userID := sess.ClientReferenceID
	if userID == "" {
		userID = sess.Metadata["user_id"]
	}
	if userID == "" {
		user, err := s.store.GetUserByStripeCustomerID(ctx, sess.Customer.ID)
		if err != nil {
			return nil, fmt.Errorf("get user by stripe customer id %s: %w", sess.Customer.ID, err)
		}
		return user, nil
	}

	user, err := s.store.GetUserByID(ctx, userID)
	if err != nil {
		return nil, fmt.Errorf("get user %s from checkout reference: %w", userID, err)
	}
	if user.StripeCustomerID == nil {
		if _, err := s.store.SetStripeCustomerIDIfUnset(ctx, user.ID, sess.Customer.ID); err != nil {
			s.log.Warn("backfill stripe customer id", "user_id", user.ID, "err", err)
		}
	}
	return user, nil
}

func (s *Server) handleSubscriptionUpdated(ctx context.Context, event *stripe.Event) error {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}
	switch sub.Status {
	case stripe.SubscriptionStatusActive, stripe.SubscriptionStatusTrialing, stripe.SubscriptionStatusPastDue:
		// past_due is a grace period: Stripe keeps retrying the charge, and moves to unpaid/canceled if it fails.
		return s.reactivateStripeSubscription(ctx, sub.ID)
	default:
		return s.cancelStripeSubscription(ctx, sub.ID)
	}
}

func (s *Server) reactivateStripeSubscription(ctx context.Context, stripeSubID string) error {
	userID, err := s.store.ReactivateSubscriptionBySourceRef(ctx, stripeSubID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // already active or unknown
		}
		return fmt.Errorf("reactivate subscription %s: %w", stripeSubID, err)
	}
	s.log.Info("stripe subscription reactivated", "user_id", userID, "subscription_id", stripeSubID)
	return nil
}

func (s *Server) handleSubscriptionDeleted(ctx context.Context, event *stripe.Event) error {
	var sub stripe.Subscription
	if err := json.Unmarshal(event.Data.Raw, &sub); err != nil {
		return fmt.Errorf("unmarshal subscription: %w", err)
	}
	return s.cancelStripeSubscription(ctx, sub.ID)
}

func (s *Server) cancelStripeSubscription(ctx context.Context, stripeSubID string) error {
	userID, err := s.store.CancelSubscriptionBySourceRef(ctx, stripeSubID)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil // already cancelled or unknown — safe to ignore
		}
		return fmt.Errorf("cancel subscription %s: %w", stripeSubID, err)
	}
	s.log.Info("stripe subscription cancelled", "user_id", userID, "subscription_id", stripeSubID)
	return nil
}

// ── OTP helpers ───────────────────────────────────────────────────────────────

// issueCode generates an OTP, stores its hash, and publishes an email event.
// Returns ResourceExhausted if the resend cooldown has not elapsed.
func (s *Server) issueCode(ctx context.Context, user *domain.User, purpose string) error {
	// Enforce resend cooldown.
	latest, err := s.store.GetLatestVerificationCode(ctx, user.ID, purpose)
	if err != nil && !errors.Is(err, store.ErrNotFound) {
		s.log.Error("get latest code", "err", err)
		return status.Error(codes.Internal, "request failed")
	}
	if latest != nil {
		elapsed := time.Since(latest.CreatedAt)
		if elapsed < s.codeCfg.ResendCooldown {
			wait := int((s.codeCfg.ResendCooldown - elapsed).Seconds()) + 1
			return status.Errorf(codes.ResourceExhausted, "please wait %d seconds before requesting another code", wait)
		}
	}

	code, err := generateOTP()
	if err != nil {
		s.log.Error("generate otp", "err", err)
		return status.Error(codes.Internal, "request failed")
	}

	expiresAt := time.Now().UTC().Add(s.codeCfg.TTL)
	if err := s.store.CreateVerificationCode(ctx, uuid.New().String(), user.ID, purpose, hashToken(code), expiresAt); err != nil {
		s.log.Error("create verification code", "err", err)
		return status.Error(codes.Internal, "request failed")
	}

	// Publish email event (best-effort; user can re-request if delivery fails).
	if s.producer != nil {
		ev := events.AuthEmailEvent{
			EventID:     uuid.New().String(),
			UserID:      user.ID,
			Email:       user.Email,
			Purpose:     purpose,
			Code:        code,
			Language:    user.Language,
			ExpiresMins: int(s.codeCfg.TTL.Minutes()),
			OccurredAt:  time.Now().UTC(),
		}
		if err := s.producer.Publish(ctx, events.AuthEmailTopic, user.ID, ev); err != nil {
			s.log.Error("publish auth email event", "purpose", purpose, "user_id", user.ID, "err", err)
		}
	} else {
		s.log.Warn("kafka producer not configured — email not sent", "purpose", purpose, "user_id", user.ID, "code", code)
	}
	return nil
}

// verifyCode spends an attempt on the latest live code, then compares the hash, so concurrent
// guesses cannot exceed MaxAttempts. A wrong final guess burns the code. It does not consume a
// correct one. Returns Unauthenticated on wrong/expired code, ResourceExhausted on the last wrong attempt.
func (s *Server) verifyCode(ctx context.Context, userID, purpose, submitted string) (*domain.VerificationCode, error) {
	vc, err := s.store.ClaimCodeAttempt(ctx, userID, purpose, s.codeCfg.MaxAttempts)
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "invalid or expired code")
		}
		s.log.Error("claim code attempt", "err", err)
		return nil, status.Error(codes.Internal, "verification failed")
	}

	if hashToken(submitted) != vc.CodeHash {
		if int(vc.Attempts) >= s.codeCfg.MaxAttempts {
			_ = s.store.ConsumeCode(ctx, vc.ID)
			return nil, status.Error(codes.ResourceExhausted, "too many incorrect attempts — request a new code")
		}
		return nil, status.Error(codes.Unauthenticated, "invalid or expired code")
	}
	return vc, nil
}

// consumeCode marks a verified code used; losing the race to a concurrent request is a bad code.
func (s *Server) consumeCode(ctx context.Context, codeID string) error {
	if err := s.store.ConsumeCode(ctx, codeID); err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return status.Error(codes.Unauthenticated, "invalid or expired code")
		}
		s.log.Error("consume code", "err", err)
		return status.Error(codes.Internal, "verification failed")
	}
	return nil
}

// verifyAndConsumeCode is verifyCode followed by consumeCode.
func (s *Server) verifyAndConsumeCode(ctx context.Context, userID, purpose, submitted string) error {
	vc, err := s.verifyCode(ctx, userID, purpose, submitted)
	if err != nil {
		return err
	}
	return s.consumeCode(ctx, vc.ID)
}

// generateOTP returns a zero-padded 6-digit numeric code from crypto/rand.
func generateOTP() (string, error) {
	var b [3]byte
	if _, err := io.ReadFull(rand.Reader, b[:]); err != nil {
		return "", fmt.Errorf("generate otp: %w", err)
	}
	n := (int(b[0])<<16 | int(b[1])<<8 | int(b[2])) % 1_000_000
	return fmt.Sprintf("%06d", n), nil
}

// hashToken hashes a token for storage. We never store raw tokens.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
