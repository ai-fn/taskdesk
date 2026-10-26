-- Single statement per file: CONCURRENTLY cannot run inside a transaction.
-- Non-unique for now: it becomes UNIQUE once legacy case-duplicates are merged.
CREATE INDEX CONCURRENTLY IF NOT EXISTS users_email_lower ON auth.users (lower(email));
