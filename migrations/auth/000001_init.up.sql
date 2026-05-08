CREATE SCHEMA IF NOT EXISTS auth;

CREATE TABLE auth.users (
    id            TEXT        PRIMARY KEY,
    email         TEXT        NOT NULL UNIQUE,
    name          TEXT        NOT NULL,
    password_hash TEXT        NOT NULL,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Refresh tokens: one row per active session.
-- token_hash is SHA-256(raw_refresh_jwt) — we never store the raw token.
CREATE TABLE auth.refresh_tokens (
    token_hash TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    expires_at TIMESTAMPTZ NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_refresh_tokens_user_id ON auth.refresh_tokens(user_id);
-- Clean up expired tokens periodically (pg_cron or app-level job in Phase 5).
CREATE INDEX idx_refresh_tokens_expires_at ON auth.refresh_tokens(expires_at);
