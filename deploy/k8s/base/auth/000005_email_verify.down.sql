DROP TABLE IF EXISTS auth.verification_codes;
ALTER TABLE auth.users DROP COLUMN IF EXISTS email_verified;
