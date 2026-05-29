-- User language preference. Validated at the application layer (not via CHECK
-- constraint) so that adding a new locale only requires a code change, not a
-- migration.
ALTER TABLE auth.users ADD COLUMN language text NOT NULL DEFAULT 'en';
