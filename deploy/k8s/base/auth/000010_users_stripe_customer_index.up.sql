-- Single statement per file: CONCURRENTLY cannot run inside a transaction.
CREATE UNIQUE INDEX CONCURRENTLY IF NOT EXISTS uq_users_stripe_customer_id ON auth.users (stripe_customer_id) WHERE stripe_customer_id IS NOT NULL;
