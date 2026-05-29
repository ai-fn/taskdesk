ALTER INDEX auth.idx_sessions_user_id    RENAME TO idx_refresh_tokens_user_id;
ALTER INDEX auth.idx_sessions_expires_at RENAME TO idx_refresh_tokens_expires_at;

ALTER TABLE auth.sessions DROP CONSTRAINT sessions_token_hash_key;
ALTER TABLE auth.sessions DROP CONSTRAINT sessions_pkey;
ALTER TABLE auth.sessions ADD PRIMARY KEY (token_hash);

ALTER TABLE auth.sessions DROP COLUMN last_active_at;
ALTER TABLE auth.sessions DROP COLUMN user_agent;
ALTER TABLE auth.sessions DROP COLUMN ip_address;
ALTER TABLE auth.sessions DROP COLUMN id;

ALTER TABLE auth.sessions RENAME TO refresh_tokens;
