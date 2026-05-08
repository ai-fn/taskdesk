// Package store handles all database operations for the auth service.
// It uses plain pgx (no ORM, no sqlc yet — sqlc is wired per-service in Phase 1).
package store

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrEmailTaken = errors.New("email already registered")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

func (s *Store) CreateUser(ctx context.Context, u *domain.User) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (id, email, name, password_hash, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		u.ID, u.Email, u.Name, u.PasswordHash, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return fmt.Errorf("insert user: %w", err)
	}
	return nil
}

func (s *Store) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, password_hash, created_at, updated_at
		FROM auth.users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by email: %w", err)
	}
	return &u, nil
}

func (s *Store) GetUserByID(ctx context.Context, id string) (*domain.User, error) {
	var u domain.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, password_hash, created_at, updated_at
		FROM auth.users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

// SaveRefreshToken stores a refresh token hash in Redis-backed session store.
// For simplicity in Phase 1 we store in Postgres; Phase 2 moves to Redis.
func (s *Store) SaveRefreshToken(ctx context.Context, userID, tokenHash string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.refresh_tokens (user_id, token_hash, expires_at)
		VALUES ($1, $2, $3)
		ON CONFLICT (token_hash) DO NOTHING`,
		userID, tokenHash, expiresAt,
	)
	return err
}

// LookupRefreshToken returns the userID for a token hash, or ErrNotFound.
// Also deletes the token (rotation: each refresh issues a new pair).
func (s *Store) LookupRefreshToken(ctx context.Context, tokenHash string) (string, error) {
	var userID string
	var expiresAt time.Time
	err := s.db.QueryRow(ctx, `
		DELETE FROM auth.refresh_tokens
		WHERE token_hash = $1
		RETURNING user_id, expires_at`,
		tokenHash,
	).Scan(&userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("lookup refresh token: %w", err)
	}
	if time.Now().After(expiresAt) {
		return "", ErrNotFound
	}
	return userID, nil
}

// DeleteRefreshToken revokes a specific token (used on logout).
func (s *Store) DeleteRefreshToken(ctx context.Context, tokenHash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.refresh_tokens WHERE token_hash = $1`, tokenHash)
	return err
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
