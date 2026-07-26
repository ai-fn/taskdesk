-- OAuth-only users have no password; existing email/password users keep theirs.
ALTER TABLE auth.users ALTER COLUMN password_hash DROP NOT NULL;

CREATE TABLE auth.oauth_identities (
    provider          TEXT        NOT NULL,                                       -- "google" | "discord"
    provider_user_id  TEXT        NOT NULL,                                       -- provider's stable user ID (Google's sub)
    user_id           TEXT        NOT NULL REFERENCES auth.users(id) ON DELETE CASCADE,
    email             TEXT        NOT NULL,                                       -- email at link time
    created_at        TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, provider_user_id)
);

CREATE INDEX idx_oauth_identities_user ON auth.oauth_identities(user_id);
