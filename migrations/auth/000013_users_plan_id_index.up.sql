-- Single statement per file: CONCURRENTLY cannot run inside a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS users_plan_id ON auth.users (plan, id);
