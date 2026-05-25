DROP TABLE IF EXISTS auth.oauth_identities;

-- Restoring NOT NULL requires no rows have NULL password_hash.
DELETE FROM auth.users WHERE password_hash IS NULL;
ALTER TABLE auth.users ALTER COLUMN password_hash SET NOT NULL;
