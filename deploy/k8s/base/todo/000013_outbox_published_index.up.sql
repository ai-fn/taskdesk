-- Single statement per file: CONCURRENTLY cannot run inside a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbox_published_at ON todo.outbox (published_at) WHERE published_at IS NOT NULL;
