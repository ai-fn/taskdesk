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
		INSERT INTO auth.users (id, email, name, password_hash, language, created_at, updated_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7)`,
		u.ID, u.Email, u.Name, u.PasswordHash, u.Language, u.CreatedAt, u.UpdatedAt,
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
		SELECT id, email, name, password_hash, language, created_at, updated_at
		FROM auth.users WHERE email = $1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.CreatedAt, &u.UpdatedAt)
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
		SELECT id, email, name, password_hash, language, created_at, updated_at
		FROM auth.users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by id: %w", err)
	}
	return &u, nil
}

// CreateSession inserts a new session row with full metadata.
func (s *Store) CreateSession(ctx context.Context, id, userID, tokenHash, ipAddress, userAgent string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.sessions (id, user_id, token_hash, ip_address, user_agent, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6)`,
		id, userID, tokenHash, ipAddress, userAgent, expiresAt,
	)
	return err
}

// RotateSession atomically rotates a session's token hash in-place (on refresh).
// The session id stays stable; last_active_at is bumped. Returns the session id
// and user id from the matched row, or ErrNotFound if no live row matched.
func (s *Store) RotateSession(ctx context.Context, oldHash, newHash string, newExpiry time.Time) (sessionID, userID string, err error) {
	var expiresAt time.Time
	err = s.db.QueryRow(ctx, `
		UPDATE auth.sessions
		SET token_hash = $2, last_active_at = now(), expires_at = $3
		WHERE token_hash = $1
		RETURNING id, user_id, expires_at`,
		oldHash, newHash, newExpiry,
	).Scan(&sessionID, &userID, &expiresAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", "", ErrNotFound
		}
		return "", "", fmt.Errorf("rotate session: %w", err)
	}
	if time.Now().After(expiresAt) {
		return "", "", ErrNotFound
	}
	return sessionID, userID, nil
}

// DeleteSessionByTokenHash deletes a session by its current token hash (logout).
func (s *Store) DeleteSessionByTokenHash(ctx context.Context, tokenHash string) error {
	_, err := s.db.Exec(ctx, `DELETE FROM auth.sessions WHERE token_hash = $1`, tokenHash)
	return err
}

// ListSessions returns all non-expired sessions for a user, newest-active first.
func (s *Store) ListSessions(ctx context.Context, userID string) ([]*domain.Session, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, token_hash, COALESCE(ip_address::TEXT, ''), COALESCE(user_agent, ''),
		       created_at, last_active_at, expires_at
		FROM auth.sessions
		WHERE user_id = $1 AND expires_at > now()
		ORDER BY last_active_at DESC`,
		userID,
	)
	if err != nil {
		return nil, fmt.Errorf("list sessions: %w", err)
	}
	defer rows.Close()

	var sessions []*domain.Session
	for rows.Next() {
		var sess domain.Session
		if err := rows.Scan(
			&sess.ID, &sess.UserID, &sess.TokenHash,
			&sess.IPAddress, &sess.UserAgent,
			&sess.CreatedAt, &sess.LastActiveAt, &sess.ExpiresAt,
		); err != nil {
			return nil, fmt.Errorf("scan session: %w", err)
		}
		sessions = append(sessions, &sess)
	}
	return sessions, rows.Err()
}

// RevokeSession deletes a session by id, scoped to the owning user.
// Returns ErrNotFound if no matching row exists.
func (s *Store) RevokeSession(ctx context.Context, userID, sessionID string) error {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM auth.sessions WHERE id = $1 AND user_id = $2`,
		sessionID, userID,
	)
	if err != nil {
		return fmt.Errorf("revoke session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeOtherSessions deletes all sessions for a user except the current one.
// Returns the number of rows deleted.
func (s *Store) RevokeOtherSessions(ctx context.Context, userID, currentSessionID string) (int64, error) {
	tag, err := s.db.Exec(ctx, `
		DELETE FROM auth.sessions WHERE user_id = $1 AND id <> $2`,
		userID, currentSessionID,
	)
	if err != nil {
		return 0, fmt.Errorf("revoke other sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ListUsers returns all users. Used by scheduler for fan-out.
func (s *Store) ListUsers(ctx context.Context) ([]*domain.User, error) {
	rows, err := s.db.Query(ctx, `SELECT id, email, name, password_hash, language, created_at, updated_at FROM auth.users ORDER BY created_at`)
	if err != nil {
		return nil, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	var users []*domain.User
	for rows.Next() {
		var u domain.User
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.CreatedAt, &u.UpdatedAt); err != nil {
			return nil, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, &u)
	}
	return users, rows.Err()
}

// GetUserLanguage returns only the language preference for the given user.
// Used by ValidateToken so every authenticated request picks up the current setting.
func (s *Store) GetUserLanguage(ctx context.Context, userID string) (string, error) {
	var lang string
	err := s.db.QueryRow(ctx, `SELECT language FROM auth.users WHERE id = $1`, userID).Scan(&lang)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "en", nil
		}
		return "en", fmt.Errorf("get user language: %w", err)
	}
	return lang, nil
}

// UpdateUserLanguage sets the preferred language for the given user.
func (s *Store) UpdateUserLanguage(ctx context.Context, userID, lang string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.users SET language = $2, updated_at = now() WHERE id = $1`,
		userID, lang,
	)
	if err != nil {
		return fmt.Errorf("update user language: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── OAuth identities ──────────────────────────────────────────────────────────

// CreateUserNoPassword inserts an OAuth-only user (password_hash NULL).
func (s *Store) CreateUserNoPassword(ctx context.Context, u *domain.User) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (id, email, name, password_hash, language, created_at, updated_at)
		VALUES ($1, $2, $3, NULL, $4, $5, $6)`,
		u.ID, u.Email, u.Name, u.Language, u.CreatedAt, u.UpdatedAt,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return fmt.Errorf("insert oauth user: %w", err)
	}
	return nil
}

// FindByOAuth returns the user linked to (provider, providerUserID).
// Returns (nil, ErrNotFound) when the identity is not linked yet.
func (s *Store) FindByOAuth(ctx context.Context, provider, providerUserID string) (*domain.User, error) {
	var u domain.User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.password_hash, u.language, u.created_at, u.updated_at
		FROM auth.oauth_identities oi
		JOIN auth.users u ON u.id = oi.user_id
		WHERE oi.provider = $1 AND oi.provider_user_id = $2`,
		provider, providerUserID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("find by oauth: %w", err)
	}
	return &u, nil
}

// LinkOAuth attaches an OAuth identity to an existing user.
// Idempotent: ON CONFLICT DO NOTHING so re-linking is a no-op.
func (s *Store) LinkOAuth(ctx context.Context, userID, provider, providerUserID, email string) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.oauth_identities (provider, provider_user_id, user_id, email)
		VALUES ($1, $2, $3, $4)
		ON CONFLICT (provider, provider_user_id) DO NOTHING`,
		provider, providerUserID, userID, email,
	)
	if err != nil {
		return fmt.Errorf("link oauth: %w", err)
	}
	return nil
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
