-- Claim lease and poison marker for the outbox publisher. Additive: an older binary still works.
ALTER TABLE todo.outbox
    ADD COLUMN claimed_until TIMESTAMPTZ,
    ADD COLUMN failed_at     TIMESTAMPTZ,
    ADD COLUMN last_error    TEXT;
