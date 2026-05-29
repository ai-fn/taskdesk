// Package server implements the AuthService gRPC server.
package server

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"time"

	ua "github.com/mileusna/useragent"
	"github.com/fsociety/todo/pkg/i18n"
	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type Server struct {
	authv1.UnimplementedAuthServiceServer
	store  *store.Store
	tokens *tokens.Maker
	log    *slog.Logger
}

func New(st *store.Store, tm *tokens.Maker, log *slog.Logger) *Server {
	return &Server{store: st, tokens: tm, log: log}
}

// ── SignUp ────────────────────────────────────────────────────────────────────

func (s *Server) SignUp(ctx context.Context, req *authv1.SignUpRequest) (*authv1.SignUpResponse, error) {
	if req.Email == "" || req.Password == "" || req.Name == "" {
		return nil, status.Error(codes.InvalidArgument, "email, password and name are required")
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

	if err := s.createSession(ctx, sessionID, user.ID, refresh, req.ClientInfo); err != nil {
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

	if err := s.createSession(ctx, sessionID, user.ID, refresh, req.ClientInfo); err != nil {
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
	claims, err := s.tokens.Verify(req.AccessToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, fmt.Sprintf("invalid token: %v", err))
	}
	lang, _ := s.store.GetUserLanguage(ctx, claims.UserID)
	return &authv1.ValidateTokenResponse{
		UserId:    claims.UserID,
		Email:     claims.Email,
		SessionId: claims.SessionID,
		Language:  lang,
	}, nil
}

// ── ListUserIDs ───────────────────────────────────────────────────────────────

func (s *Server) ListUserIDs(ctx context.Context, _ *authv1.ListUserIDsRequest) (*authv1.ListUserIDsResponse, error) {
	users, err := s.store.ListUsers(ctx)
	if err != nil {
		s.log.Error("list users", "err", err)
		return nil, status.Error(codes.Internal, "list users failed")
	}
	resp := &authv1.ListUserIDsResponse{}
	for _, u := range users {
		resp.Users = append(resp.Users, &authv1.UserSummary{
			UserId:   u.ID,
			Email:    u.Email,
			Name:     u.Name,
			Language: u.Language,
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
			UserId:   u.ID,
			Email:    u.Email,
			Name:     u.Name,
			Language: u.Language,
		},
	}, nil
}

// ── OAuthSignIn ───────────────────────────────────────────────────────────────

// OAuthSignIn handles a verified OAuth identity from the gateway.
// Decision tree:
//  1. Identity already linked → return existing user, created=false.
//  2. Email matches existing user → link identity, created=false.
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
		user = u
	} else if !errors.Is(err, store.ErrNotFound) {
		s.log.Error("find by oauth", "err", err)
		return nil, status.Error(codes.Internal, "oauth signin failed")
	}

	// 2. Match by email?
	if user == nil {
		existing, err := s.store.GetUserByEmail(ctx, req.Email)
		if err == nil {
			if err := s.store.LinkOAuth(ctx, existing.ID, req.Provider, req.ProviderUserId, req.Email); err != nil {
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
				if err := s.store.LinkOAuth(ctx, existing.ID, req.Provider, req.ProviderUserId, req.Email); err != nil {
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
	expiresAt := time.Now().UTC().Add(tokens.RefreshTokenTTL)
	ip, agent := "", ""
	if info != nil {
		ip = info.IpAddress
		agent = info.UserAgent
	}
	if err := s.store.CreateSession(ctx, sessionID, userID, hashToken(refreshToken), ip, agent, expiresAt); err != nil {
		s.log.Error("create session", "err", err)
		return status.Error(codes.Internal, "save session failed")
	}
	return nil
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

// hashToken hashes a token for storage. We never store raw tokens.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
