ALTER TABLE todo.outbox
    DROP COLUMN IF EXISTS last_error,
    DROP COLUMN IF EXISTS failed_at,
    DROP COLUMN IF EXISTS claimed_until;
