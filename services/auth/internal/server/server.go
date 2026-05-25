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

	"github.com/fsociety/todo/pkg/tokens"
	authv1 "github.com/fsociety/todo/proto/gen/auth/v1"
	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/fsociety/todo/services/auth/internal/store"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
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

	access, refresh, err := s.mintTokens(user.ID, user.Email)
	if err != nil {
		return nil, err
	}

	if err := s.saveRefreshToken(ctx, user.ID, refresh); err != nil {
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

	access, refresh, err := s.mintTokens(user.ID, user.Email)
	if err != nil {
		return nil, err
	}

	if err := s.saveRefreshToken(ctx, user.ID, refresh); err != nil {
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

	// Lookup + rotate (deletes old token atomically).
	userID, err := s.store.LookupRefreshToken(ctx, hashToken(req.RefreshToken))
	if err != nil {
		if errors.Is(err, store.ErrNotFound) {
			return nil, status.Error(codes.Unauthenticated, "refresh token revoked or expired")
		}
		s.log.Error("lookup refresh token", "err", err)
		return nil, status.Error(codes.Internal, "refresh failed")
	}

	access, refresh, err := s.mintTokens(userID, claims.Email)
	if err != nil {
		return nil, err
	}

	if err := s.saveRefreshToken(ctx, userID, refresh); err != nil {
		return nil, err
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
	_ = s.store.DeleteRefreshToken(ctx, hashToken(req.RefreshToken))
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

func (s *Server) ValidateToken(_ context.Context, req *authv1.ValidateTokenRequest) (*authv1.ValidateTokenResponse, error) {
	if req.AccessToken == "" {
		return nil, status.Error(codes.InvalidArgument, "access_token is required")
	}
	claims, err := s.tokens.Verify(req.AccessToken)
	if err != nil {
		return nil, status.Error(codes.Unauthenticated, fmt.Sprintf("invalid token: %v", err))
	}
	return &authv1.ValidateTokenResponse{
		UserId: claims.UserID,
		Email:  claims.Email,
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
			UserId: u.ID,
			Email:  u.Email,
			Name:   u.Name,
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
			UserId: u.ID,
			Email:  u.Email,
			Name:   u.Name,
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

	access, refresh, err := s.mintTokens(user.ID, user.Email)
	if err != nil {
		return nil, err
	}
	if err := s.saveRefreshToken(ctx, user.ID, refresh); err != nil {
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

// ── helpers ───────────────────────────────────────────────────────────────────

func (s *Server) mintTokens(userID, email string) (access, refresh string, err error) {
	access, err = s.tokens.CreateAccessToken(userID, email)
	if err != nil {
		s.log.Error("create access token", "err", err)
		return "", "", status.Error(codes.Internal, "token creation failed")
	}
	refresh, err = s.tokens.CreateRefreshToken(userID, email)
	if err != nil {
		s.log.Error("create refresh token", "err", err)
		return "", "", status.Error(codes.Internal, "token creation failed")
	}
	return access, refresh, nil
}

func (s *Server) saveRefreshToken(ctx context.Context, userID, refreshToken string) error {
	expiresAt := time.Now().UTC().Add(tokens.RefreshTokenTTL)
	if err := s.store.SaveRefreshToken(ctx, userID, hashToken(refreshToken), expiresAt); err != nil {
		s.log.Error("save refresh token", "err", err)
		return status.Error(codes.Internal, "save token failed")
	}
	return nil
}

// hashToken hashes a token for storage. We never store raw tokens.
func hashToken(token string) string {
	h := sha256.Sum256([]byte(token))
	return hex.EncodeToString(h[:])
}
