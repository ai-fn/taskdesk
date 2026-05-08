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

	now := time.Now().UTC()
	user := &domain.User{
		ID:           uuid.New().String(),
		Email:        req.Email,
		Name:         req.Name,
		PasswordHash: string(hash),
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

	if err := bcrypt.CompareHashAndPassword([]byte(user.PasswordHash), []byte(req.Password)); err != nil {
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
