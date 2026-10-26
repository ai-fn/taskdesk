// Package store handles all database operations for the auth service.
// It uses plain pgx (no ORM, no sqlc yet — sqlc is wired per-service in Phase 1).
package store

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/fsociety/todo/services/auth/internal/domain"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"
)

var ErrNotFound = errors.New("not found")
var ErrEmailTaken = errors.New("email already registered")
var ErrInvalidEmail = errors.New("invalid email")
var ErrSubscriptionAlreadyExists = errors.New("subscription already exists")
var ErrPromoCodeNotFound = errors.New("promo code not found")
var ErrPromoCodeDisabled = errors.New("promo code is disabled")
var ErrPromoCodeEmailMismatch = errors.New("promo code is not valid for your account")
var ErrPromoCodeExhausted = errors.New("promo code has reached its usage limit")
var ErrPromoCodePerUserLimitReached = errors.New("you have already used this promo code the maximum number of times")
var ErrUserHasActiveStripeSubscription = errors.New("user has an active Stripe subscription")

type Store struct {
	db *pgxpool.Pool
}

func New(db *pgxpool.Pool) *Store {
	return &Store{db: db}
}

// NormalizeEmail is the canonical form of an address: accounts are matched case-insensitively.
func NormalizeEmail(email string) string { return strings.ToLower(strings.TrimSpace(email)) }

// CreateUser stores the user with a lowercased email (u.Email is updated to match). The NOT EXISTS
// also refuses an address held under another casing by a pre-normalization row.
func (s *Store) CreateUser(ctx context.Context, u *domain.User) error {
	u.Email = NormalizeEmail(u.Email)
	if u.Email == "" {
		return ErrInvalidEmail
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (id, email, name, password_hash, language, email_verified, created_at, updated_at)
		SELECT $1, $2, $3, $4, $5, $6, $7, $8
		WHERE NOT EXISTS (SELECT 1 FROM auth.users WHERE lower(email) = $2)`,
		u.ID, u.Email, u.Name, u.PasswordHash, u.Language, u.EmailVerified, u.CreatedAt, u.UpdatedAt,
	)
	// plan and stripe_customer_id default to 'free'/NULL via column defaults
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return fmt.Errorf("insert user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEmailTaken
	}
	return nil
}

// GetUserByEmail matches case-insensitively; if legacy rows differ only by case, the lowercase
// row wins, then the oldest. Operators must merge such duplicates (see migration 000012).
func (s *Store) GetUserByEmail(ctx context.Context, email string) (*domain.User, error) {
	var u domain.User
	email = NormalizeEmail(email)
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, password_hash, language, email_verified, plan, stripe_customer_id, created_at, updated_at
		FROM auth.users WHERE lower(email) = $1
		ORDER BY (email = $1) DESC, created_at
		LIMIT 1`, email,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.EmailVerified, &u.Plan, &u.StripeCustomerID, &u.CreatedAt, &u.UpdatedAt)
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
		SELECT id, email, name, password_hash, language, email_verified, plan, stripe_customer_id, created_at, updated_at
		FROM auth.users WHERE id = $1`, id,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.EmailVerified, &u.Plan, &u.StripeCustomerID, &u.CreatedAt, &u.UpdatedAt)
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

// CreateLoginSession is CreateSession guarded on the password hash the caller verified.
// FOR SHARE makes it wait for a concurrent ClaimUnverifiedAccount, so no session outlives the claim.
func (s *Store) CreateLoginSession(ctx context.Context, id, userID, tokenHash, ipAddress, userAgent string, expiresAt time.Time, passwordHash string) error {
	tag, err := s.db.Exec(ctx, `
		INSERT INTO auth.sessions (id, user_id, token_hash, ip_address, user_agent, expires_at)
		SELECT $1::uuid, id, $3::text, $4::inet, $5::text, $6::timestamptz
		FROM auth.users WHERE id = $2 AND password_hash = $7
		FOR SHARE`,
		id, userID, tokenHash, ipAddress, userAgent, expiresAt, passwordHash,
	)
	if err != nil {
		return err
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
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

// SessionActive reports whether a non-expired session with this id exists.
func (s *Store) SessionActive(ctx context.Context, sessionID string) (bool, error) {
	var active bool
	err := s.db.QueryRow(ctx, `
		SELECT EXISTS (SELECT 1 FROM auth.sessions WHERE id = $1 AND expires_at > now())`,
		sessionID,
	).Scan(&active)
	if err != nil {
		return false, fmt.Errorf("session active: %w", err)
	}
	return active, nil
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

// UserSummary is the listing view of a user: no credentials or billing ids.
type UserSummary struct {
	ID, Email, Name, Language, Plan string
	EmailVerified                   bool
}

// ListUserSummaries returns up to limit users with id > afterID in id order, optionally filtered
// by plan ("" = all), plus whether more remain. Used by the scheduler and AI backfills.
func (s *Store) ListUserSummaries(ctx context.Context, planFilter, afterID string, limit int) ([]UserSummary, bool, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, email, name, language, email_verified, plan
		FROM auth.users
		WHERE id > $1 AND ($2 = '' OR plan = $2)
		ORDER BY id
		LIMIT $3`, afterID, planFilter, limit+1)
	if err != nil {
		return nil, false, fmt.Errorf("list users: %w", err)
	}
	defer rows.Close()

	users := make([]UserSummary, 0, limit)
	for rows.Next() {
		var u UserSummary
		if err := rows.Scan(&u.ID, &u.Email, &u.Name, &u.Language, &u.EmailVerified, &u.Plan); err != nil {
			return nil, false, fmt.Errorf("scan user: %w", err)
		}
		users = append(users, u)
	}
	if err := rows.Err(); err != nil {
		return nil, false, err
	}
	more := len(users) > limit
	if more {
		users = users[:limit]
	}
	return users, more, nil
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
	u.Email = NormalizeEmail(u.Email)
	if u.Email == "" {
		return ErrInvalidEmail
	}
	tag, err := s.db.Exec(ctx, `
		INSERT INTO auth.users (id, email, name, password_hash, language, email_verified, created_at, updated_at)
		SELECT $1, $2, $3, NULL, $4, true, $5, $6
		WHERE NOT EXISTS (SELECT 1 FROM auth.users WHERE lower(email) = $2)`,
		u.ID, u.Email, u.Name, u.Language, u.CreatedAt, u.UpdatedAt,
	)
	// plan defaults to 'free' via column default
	if err != nil {
		if isUniqueViolation(err) {
			return ErrEmailTaken
		}
		return fmt.Errorf("insert oauth user: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrEmailTaken
	}
	return nil
}

// FindByOAuth returns the user linked to (provider, providerUserID).
// Returns (nil, ErrNotFound) when the identity is not linked yet.
func (s *Store) FindByOAuth(ctx context.Context, provider, providerUserID string) (*domain.User, error) {
	var u domain.User
	err := s.db.QueryRow(ctx, `
		SELECT u.id, u.email, u.name, u.password_hash, u.language, u.email_verified, u.plan, u.stripe_customer_id, u.created_at, u.updated_at
		FROM auth.oauth_identities oi
		JOIN auth.users u ON u.id = oi.user_id
		WHERE oi.provider = $1 AND oi.provider_user_id = $2`,
		provider, providerUserID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.EmailVerified, &u.Plan, &u.StripeCustomerID, &u.CreatedAt, &u.UpdatedAt)
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
		provider, providerUserID, userID, NormalizeEmail(email),
	)
	if err != nil {
		return fmt.Errorf("link oauth: %w", err)
	}
	return nil
}

// ── Verification codes ────────────────────────────────────────────────────────

// CreateVerificationCode inserts a new verification code row.
func (s *Store) CreateVerificationCode(ctx context.Context, id, userID, purpose, codeHash string, expiresAt time.Time) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.verification_codes (id, user_id, purpose, code_hash, expires_at)
		VALUES ($1, $2, $3, $4, $5)`,
		id, userID, purpose, codeHash, expiresAt,
	)
	if err != nil {
		return fmt.Errorf("create verification code: %w", err)
	}
	return nil
}

// GetLatestVerificationCode returns the most recently created code for a user+purpose,
// regardless of consumed or expired state (used to enforce resend cooldown).
func (s *Store) GetLatestVerificationCode(ctx context.Context, userID, purpose string) (*domain.VerificationCode, error) {
	var c domain.VerificationCode
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, purpose, code_hash, attempts, expires_at, consumed_at, created_at
		FROM auth.verification_codes
		WHERE user_id = $1 AND purpose = $2
		ORDER BY created_at DESC
		LIMIT 1`,
		userID, purpose,
	).Scan(&c.ID, &c.UserID, &c.Purpose, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &c.ConsumedAt, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get latest verification code: %w", err)
	}
	return &c, nil
}

// GetActiveVerificationCode returns the most recent unconsumed, unexpired code
// for the given user+purpose. Returns ErrNotFound if none exists.
func (s *Store) GetActiveVerificationCode(ctx context.Context, userID, purpose string) (*domain.VerificationCode, error) {
	var c domain.VerificationCode
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, purpose, code_hash, attempts, expires_at, consumed_at, created_at
		FROM auth.verification_codes
		WHERE user_id = $1 AND purpose = $2
		  AND consumed_at IS NULL AND expires_at > now()
		ORDER BY created_at DESC
		LIMIT 1`,
		userID, purpose,
	).Scan(&c.ID, &c.UserID, &c.Purpose, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &c.ConsumedAt, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get active verification code: %w", err)
	}
	return &c, nil
}

// ClaimCodeAttempt spends one attempt on the latest live code for user+purpose and returns it
// with the new count. The attempts cap sits in the UPDATE's own WHERE so concurrent guesses
// re-check it after the row lock: at most maxAttempts callers ever get the hash.
// Returns ErrNotFound if there is no live code or its attempts are used up.
func (s *Store) ClaimCodeAttempt(ctx context.Context, userID, purpose string, maxAttempts int) (*domain.VerificationCode, error) {
	var c domain.VerificationCode
	err := s.db.QueryRow(ctx, `
		UPDATE auth.verification_codes SET attempts = attempts + 1
		WHERE id = (
			SELECT id FROM auth.verification_codes
			WHERE user_id = $1 AND purpose = $2 AND consumed_at IS NULL AND expires_at > now()
			ORDER BY created_at DESC
			LIMIT 1)
		  AND consumed_at IS NULL AND attempts < $3
		RETURNING id, user_id, purpose, code_hash, attempts, expires_at, consumed_at, created_at`,
		userID, purpose, maxAttempts,
	).Scan(&c.ID, &c.UserID, &c.Purpose, &c.CodeHash, &c.Attempts, &c.ExpiresAt, &c.ConsumedAt, &c.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("claim code attempt: %w", err)
	}
	return &c, nil
}

// ConsumeCode atomically marks a verification code as used.
// Returns ErrNotFound if the code was already consumed or doesn't exist,
// preventing a TOCTOU race where two concurrent correct submissions both succeed.
func (s *Store) ConsumeCode(ctx context.Context, codeID string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.verification_codes SET consumed_at = now()
		WHERE id = $1 AND consumed_at IS NULL`,
		codeID,
	)
	if err != nil {
		return fmt.Errorf("consume code: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// ── User mutations ────────────────────────────────────────────────────────────

// SetEmailVerified marks the user's email as verified.
func (s *Store) SetEmailVerified(ctx context.Context, userID string) error {
	_, err := s.db.Exec(ctx, `
		UPDATE auth.users SET email_verified = true, updated_at = now() WHERE id = $1`,
		userID,
	)
	return err
}

// ClaimUnverifiedAccount hands an account with an unverified email to the OAuth provider
// that proved ownership: drops the password, its sessions and pending codes, marks it verified.
// It reports false if the email was verified meanwhile.
func (s *Store) ClaimUnverifiedAccount(ctx context.Context, userID string) (bool, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return false, fmt.Errorf("claim account: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	var hadPassword bool
	err = tx.QueryRow(ctx, `
		SELECT password_hash IS NOT NULL FROM auth.users WHERE id = $1 AND NOT email_verified FOR UPDATE`,
		userID,
	).Scan(&hadPassword)
	if errors.Is(err, pgx.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("claim account: %w", err)
	}

	if _, err := tx.Exec(ctx, `
		UPDATE auth.users SET password_hash = NULL, email_verified = true, updated_at = now() WHERE id = $1`,
		userID,
	); err != nil {
		return false, fmt.Errorf("claim account: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM auth.verification_codes WHERE user_id = $1`, userID); err != nil {
		return false, fmt.Errorf("claim account: clear codes: %w", err)
	}
	// An OAuth-only account has no password for an attacker to know; keep its sessions.
	if hadPassword {
		if _, err := tx.Exec(ctx, `DELETE FROM auth.sessions WHERE user_id = $1`, userID); err != nil {
			return false, fmt.Errorf("claim account: revoke sessions: %w", err)
		}
	}
	if err := tx.Commit(ctx); err != nil {
		return false, fmt.Errorf("claim account: %w", err)
	}
	return true, nil
}

// UpdatePassword sets a new bcrypt password hash for the user.
func (s *Store) UpdatePassword(ctx context.Context, userID, passwordHash string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.users SET password_hash = $2, updated_at = now() WHERE id = $1`,
		userID, passwordHash,
	)
	if err != nil {
		return fmt.Errorf("update password: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

// RevokeAllSessions deletes every session for a user (e.g. after password reset).
func (s *Store) RevokeAllSessions(ctx context.Context, userID string) (int64, error) {
	tag, err := s.db.Exec(ctx, `DELETE FROM auth.sessions WHERE user_id = $1`, userID)
	if err != nil {
		return 0, fmt.Errorf("revoke all sessions: %w", err)
	}
	return tag.RowsAffected(), nil
}

// ── Stripe ────────────────────────────────────────────────────────────────────

// SetStripeCustomerIDIfUnset stores customerID unless the user already has one, and returns
// the id in effect. Concurrent checkouts thus converge on a single Stripe customer.
func (s *Store) SetStripeCustomerIDIfUnset(ctx context.Context, userID, customerID string) (string, error) {
	var stored string
	err := s.db.QueryRow(ctx, `
		UPDATE auth.users SET stripe_customer_id = COALESCE(stripe_customer_id, $2), updated_at = now()
		WHERE id = $1 RETURNING stripe_customer_id`,
		userID, customerID,
	).Scan(&stored)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("set stripe customer id: %w", err)
	}
	return stored, nil
}

// GetUserByStripeCustomerID looks up a user by their Stripe customer ID.
// Used when processing Stripe webhook events that carry only the customer ID.
func (s *Store) GetUserByStripeCustomerID(ctx context.Context, customerID string) (*domain.User, error) {
	var u domain.User
	err := s.db.QueryRow(ctx, `
		SELECT id, email, name, password_hash, language, email_verified, plan, stripe_customer_id, created_at, updated_at
		FROM auth.users WHERE stripe_customer_id = $1`, customerID,
	).Scan(&u.ID, &u.Email, &u.Name, &u.PasswordHash, &u.Language, &u.EmailVerified, &u.Plan, &u.StripeCustomerID, &u.CreatedAt, &u.UpdatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get user by stripe customer id: %w", err)
	}
	return &u, nil
}

// ── Subscriptions ─────────────────────────────────────────────────────────────

// CreateSubscriptionAndSetPlan atomically inserts a subscription row and updates
// auth.users.plan in a single transaction. This is the correct path for Stripe
// webhook processing: if either write fails, both are rolled back and the webhook
// can be retried safely.
//
// Returns ErrSubscriptionAlreadyExists when the source_ref unique constraint
// fires — callers should treat this as an idempotent success (already processed).
func (s *Store) CreateSubscriptionAndSetPlan(ctx context.Context, sub *domain.Subscription, plan string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	tag, err := tx.Exec(ctx, `
		INSERT INTO auth.subscriptions (id, user_id, plan, source, source_ref, status, started_at, expires_at)
		VALUES ($1, $2, $3, $4, $5, $6, $7, $8)
		ON CONFLICT (source_ref) WHERE source = 'stripe' DO NOTHING`,
		sub.ID, sub.UserID, sub.Plan, sub.Source, sub.SourceRef, sub.Status, sub.StartedAt, sub.ExpiresAt,
	)
	if err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}
	if tag.RowsAffected() == 0 {
		// Row already exists — idempotent, nothing left to do.
		return ErrSubscriptionAlreadyExists
	}

	if _, err := tx.Exec(ctx, `
		UPDATE auth.users SET plan = $2, updated_at = now() WHERE id = $1`,
		sub.UserID, plan,
	); err != nil {
		return fmt.Errorf("set user plan: %w", err)
	}

	return tx.Commit(ctx)
}

// GetActiveSubscriptions returns all active subscriptions for a user.
func (s *Store) GetActiveSubscriptions(ctx context.Context, userID string) ([]*domain.Subscription, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, user_id, plan, source, source_ref, status, started_at, expires_at, created_at, updated_at
		FROM auth.subscriptions
		WHERE user_id = $1 AND status = 'active'
		ORDER BY created_at DESC`, userID,
	)
	if err != nil {
		return nil, fmt.Errorf("get active subscriptions: %w", err)
	}
	defer rows.Close()

	var subs []*domain.Subscription
	for rows.Next() {
		var sub domain.Subscription
		if err := rows.Scan(
			&sub.ID, &sub.UserID, &sub.Plan, &sub.Source, &sub.SourceRef,
			&sub.Status, &sub.StartedAt, &sub.ExpiresAt, &sub.CreatedAt, &sub.UpdatedAt,
		); err != nil {
			return nil, fmt.Errorf("scan subscription: %w", err)
		}
		subs = append(subs, &sub)
	}
	return subs, rows.Err()
}

// GetSubscriptionBySourceRef returns a subscription by its source_ref (Stripe subscription ID).
func (s *Store) GetSubscriptionBySourceRef(ctx context.Context, sourceRef string) (*domain.Subscription, error) {
	var sub domain.Subscription
	err := s.db.QueryRow(ctx, `
		SELECT id, user_id, plan, source, source_ref, status, started_at, expires_at, created_at, updated_at
		FROM auth.subscriptions WHERE source_ref = $1`, sourceRef,
	).Scan(
		&sub.ID, &sub.UserID, &sub.Plan, &sub.Source, &sub.SourceRef,
		&sub.Status, &sub.StartedAt, &sub.ExpiresAt, &sub.CreatedAt, &sub.UpdatedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrNotFound
		}
		return nil, fmt.Errorf("get subscription by source ref: %w", err)
	}
	return &sub, nil
}

const recalculatePlanSQL = `
	UPDATE auth.users SET
	  plan = CASE
	    WHEN EXISTS (
	      SELECT 1 FROM auth.subscriptions
	      WHERE user_id = $1 AND status = 'active'
	    ) THEN 'pro'
	    ELSE 'free'
	  END,
	  updated_at = now()
	WHERE id = $1`

// CancelSubscriptionBySourceRef cancels a Stripe subscription and recalculates the user's plan
// in one transaction, so a retried webhook never finds the row changed but the plan stale.
// Returns the user id, or ErrNotFound if there is no active subscription to cancel.
func (s *Store) CancelSubscriptionBySourceRef(ctx context.Context, sourceRef string) (string, error) {
	return s.moveSubscription(ctx, sourceRef, "active", "cancelled")
}

// ReactivateSubscriptionBySourceRef is the inverse, for a payment that recovers.
// Returns ErrNotFound if there is no cancelled subscription to reactivate.
func (s *Store) ReactivateSubscriptionBySourceRef(ctx context.Context, sourceRef string) (string, error) {
	return s.moveSubscription(ctx, sourceRef, "cancelled", "active")
}

func (s *Store) moveSubscription(ctx context.Context, sourceRef, from, to string) (string, error) {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return "", fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck // no-op after Commit

	var userID string
	err = tx.QueryRow(ctx, `
		UPDATE auth.subscriptions
		SET status = $3, updated_at = now()
		WHERE source = 'stripe' AND source_ref = $1 AND status = $2
		RETURNING user_id`, sourceRef, from, to,
	).Scan(&userID)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return "", ErrNotFound
		}
		return "", fmt.Errorf("move subscription %s -> %s: %w", from, to, err)
	}
	if _, err := tx.Exec(ctx, recalculatePlanSQL, userID); err != nil {
		return "", fmt.Errorf("recalculate user plan: %w", err)
	}
	if err := tx.Commit(ctx); err != nil {
		return "", fmt.Errorf("commit: %w", err)
	}
	return userID, nil
}

// RecalculateUserPlan sets auth.users.plan to 'pro' if any active subscription
// exists for the user, otherwise downgrades to 'free'. Safe to call after any
// subscription status change.
func (s *Store) RecalculateUserPlan(ctx context.Context, userID string) error {
	if _, err := s.db.Exec(ctx, recalculatePlanSQL, userID); err != nil {
		return fmt.Errorf("recalculate user plan: %w", err)
	}
	return nil
}

// ── Promo codes ───────────────────────────────────────────────────────────────

// GetPromoCode retrieves a promo code by its code string (case-insensitive).
func (s *Store) GetPromoCode(ctx context.Context, code string) (*domain.PromoCode, error) {
	var pc domain.PromoCode
	err := s.db.QueryRow(ctx, `
		SELECT id, code, allowed_email, max_usage_count, max_usage_per_user, disabled, notes, created_at
		FROM auth.promo_codes WHERE upper(code) = upper($1)`, code,
	).Scan(&pc.ID, &pc.Code, &pc.AllowedEmail, &pc.MaxUsageCount, &pc.MaxUsagePerUser, &pc.Disabled, &pc.Notes, &pc.CreatedAt)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, ErrPromoCodeNotFound
		}
		return nil, fmt.Errorf("get promo code: %w", err)
	}
	return &pc, nil
}

// RedeemPromoCode atomically validates and redeems a promo code for a user,
// inserts a subscription record, and upgrades the user's plan to 'pro'.
// The promo code row is locked with SELECT FOR UPDATE to serialize concurrent
// redemption attempts and guarantee the usage-count checks are consistent.
func (s *Store) RedeemPromoCode(ctx context.Context, pc *domain.PromoCode, userID, userEmail, subscriptionID string) error {
	tx, err := s.db.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer tx.Rollback(ctx) //nolint:errcheck

	// Re-read the code inside the transaction under a row-level lock so concurrent
	// redemptions queue up and each sees the count after the previous committed.
	var locked domain.PromoCode
	err = tx.QueryRow(ctx, `
		SELECT id, allowed_email, max_usage_count, max_usage_per_user, disabled
		FROM auth.promo_codes WHERE id = $1 FOR UPDATE`, pc.ID,
	).Scan(&locked.ID, &locked.AllowedEmail, &locked.MaxUsageCount, &locked.MaxUsagePerUser, &locked.Disabled)
	if err != nil {
		return fmt.Errorf("lock promo code: %w", err)
	}

	if locked.Disabled {
		return ErrPromoCodeDisabled
	}
	if locked.AllowedEmail != nil && NormalizeEmail(*locked.AllowedEmail) != NormalizeEmail(userEmail) {
		return ErrPromoCodeEmailMismatch
	}

	// Block redemption if the user acquired an active Stripe subscription between
	// the server-layer pre-check and now (TOCTOU guard). Checked inside the
	// transaction so it is atomic with the subsequent INSERT.
	var hasStripe bool
	if err := tx.QueryRow(ctx,
		`SELECT EXISTS(
			SELECT 1 FROM auth.subscriptions
			WHERE user_id = $1 AND source = 'stripe' AND status = 'active'
		)`, userID,
	).Scan(&hasStripe); err != nil {
		return fmt.Errorf("check stripe subscription: %w", err)
	}
	if hasStripe {
		return ErrUserHasActiveStripeSubscription
	}

	// Fetch global and per-user redemption counts in a single round trip.
	// FILTER (WHERE user_id = $2) computes the per-user count on the same scan.
	var globalCount, userCount int
	if err := tx.QueryRow(ctx, `
		SELECT
			COUNT(*)                          AS global_count,
			COUNT(*) FILTER (WHERE user_id = $2) AS user_count
		FROM auth.promo_code_redemptions
		WHERE promo_code_id = $1`,
		locked.ID, userID,
	).Scan(&globalCount, &userCount); err != nil {
		return fmt.Errorf("count redemptions: %w", err)
	}

	if locked.MaxUsageCount != nil && globalCount >= *locked.MaxUsageCount {
		return ErrPromoCodeExhausted
	}
	if userCount >= locked.MaxUsagePerUser {
		return ErrPromoCodePerUserLimitReached
	}

	// Insert redemption record.
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.promo_code_redemptions (id, promo_code_id, user_id)
		VALUES ($1, $2, $3)`,
		uuid.New().String(), locked.ID, userID,
	); err != nil {
		return fmt.Errorf("insert redemption: %w", err)
	}

	// Insert subscription record (lifetime — expires_at NULL).
	if _, err := tx.Exec(ctx, `
		INSERT INTO auth.subscriptions (id, user_id, plan, source, source_ref, status, expires_at)
		VALUES ($1, $2, 'pro', 'promo_code', $3, 'active', NULL)`,
		subscriptionID, userID, locked.ID,
	); err != nil {
		return fmt.Errorf("insert subscription: %w", err)
	}

	// Upgrade user plan to 'pro'.
	if _, err := tx.Exec(ctx, `
		UPDATE auth.users SET plan = 'pro', updated_at = now() WHERE id = $1`, userID,
	); err != nil {
		return fmt.Errorf("set user plan: %w", err)
	}

	return tx.Commit(ctx)
}

// ListPromoCodes returns all promo codes ordered by creation date descending.
// Used by the promo-admin CLI.
func (s *Store) ListPromoCodes(ctx context.Context) ([]*domain.PromoCode, error) {
	rows, err := s.db.Query(ctx, `
		SELECT id, code, allowed_email, max_usage_count, max_usage_per_user, disabled, notes, created_at
		FROM auth.promo_codes ORDER BY created_at DESC`,
	)
	if err != nil {
		return nil, fmt.Errorf("list promo codes: %w", err)
	}
	defer rows.Close()

	var codes []*domain.PromoCode
	for rows.Next() {
		var pc domain.PromoCode
		if err := rows.Scan(&pc.ID, &pc.Code, &pc.AllowedEmail, &pc.MaxUsageCount, &pc.MaxUsagePerUser, &pc.Disabled, &pc.Notes, &pc.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan promo code: %w", err)
		}
		codes = append(codes, &pc)
	}
	return codes, rows.Err()
}

// CreatePromoCode inserts a new promo code. Used by the promo-admin CLI.
func (s *Store) CreatePromoCode(ctx context.Context, pc *domain.PromoCode) error {
	_, err := s.db.Exec(ctx, `
		INSERT INTO auth.promo_codes (id, code, allowed_email, max_usage_count, max_usage_per_user, disabled, notes)
		VALUES ($1, upper($2), $3, $4, $5, false, $6)`,
		pc.ID, pc.Code, pc.AllowedEmail, pc.MaxUsageCount, pc.MaxUsagePerUser, pc.Notes,
	)
	if err != nil {
		if isUniqueViolation(err) {
			return fmt.Errorf("promo code already exists")
		}
		return fmt.Errorf("create promo code: %w", err)
	}
	return nil
}

// DisablePromoCode marks a promo code as disabled by its code string.
func (s *Store) DisablePromoCode(ctx context.Context, code string) error {
	tag, err := s.db.Exec(ctx, `
		UPDATE auth.promo_codes SET disabled = true WHERE upper(code) = upper($1)`, code,
	)
	if err != nil {
		return fmt.Errorf("disable promo code: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrPromoCodeNotFound
	}
	return nil
}

// CountAllPromoCodeRedemptions returns redemption counts for every promo code in
// a single query, keyed by promo code ID. Used by the promo-admin CLI to avoid
// N+1 queries when listing codes.
func (s *Store) CountAllPromoCodeRedemptions(ctx context.Context) (map[string]int, error) {
	rows, err := s.db.Query(ctx,
		`SELECT promo_code_id, COUNT(*) FROM auth.promo_code_redemptions GROUP BY promo_code_id`,
	)
	if err != nil {
		return nil, fmt.Errorf("count all promo code redemptions: %w", err)
	}
	defer rows.Close()

	counts := make(map[string]int)
	for rows.Next() {
		var id string
		var n int
		if err := rows.Scan(&id, &n); err != nil {
			return nil, fmt.Errorf("scan redemption count: %w", err)
		}
		counts[id] = n
	}
	return counts, rows.Err()
}

func isUniqueViolation(err error) bool {
	var pgErr *pgconn.PgError
	return errors.As(err, &pgErr) && pgErr.Code == "23505"
}
