-- Single statement per file: CONCURRENTLY cannot run inside a transaction.
CREATE INDEX CONCURRENTLY IF NOT EXISTS outbox_pending ON todo.outbox (created_at) WHERE published_at IS NULL AND failed_at IS NULL;
