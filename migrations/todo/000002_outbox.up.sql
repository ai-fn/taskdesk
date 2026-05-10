-- Transactional outbox for task domain events.
-- Publisher worker SELECTs with FOR UPDATE SKIP LOCKED, produces to Kafka,
-- then marks published_at. Writing to this table in the same TX as the
-- task mutation guarantees at-least-once delivery with no lost events.
CREATE TABLE todo.outbox (
    id           UUID        PRIMARY KEY DEFAULT gen_random_uuid(),
    event_type   TEXT        NOT NULL,
    payload      JSONB       NOT NULL,
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now(),
    published_at TIMESTAMPTZ
);

CREATE INDEX outbox_unpublished ON todo.outbox (created_at)
    WHERE published_at IS NULL;
