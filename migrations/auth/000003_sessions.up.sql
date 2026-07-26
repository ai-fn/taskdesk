ALTER TABLE auth.refresh_tokens RENAME TO sessions;

ALTER TABLE auth.sessions ADD COLUMN id UUID NOT NULL DEFAULT gen_random_uuid();
ALTER TABLE auth.sessions ADD COLUMN ip_address INET;
ALTER TABLE auth.sessions ADD COLUMN user_agent TEXT;
ALTER TABLE auth.sessions ADD COLUMN last_active_at TIMESTAMPTZ NOT NULL DEFAULT now();

ALTER TABLE auth.sessions DROP CONSTRAINT refresh_tokens_pkey;
ALTER TABLE auth.sessions ADD PRIMARY KEY (id);
ALTER TABLE auth.sessions ADD CONSTRAINT sessions_token_hash_key UNIQUE (token_hash);

ALTER INDEX auth.idx_refresh_tokens_user_id    RENAME TO idx_sessions_user_id;
ALTER INDEX auth.idx_refresh_tokens_expires_at RENAME TO idx_sessions_expires_at;
