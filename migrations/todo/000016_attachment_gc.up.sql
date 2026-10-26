-- Last part-URL request of a pending upload; NULL = untouched since created_at.
ALTER TABLE todo.attachments ADD COLUMN IF NOT EXISTS last_activity_at TIMESTAMPTZ;

-- Reaper scan, and the per-user pending-bytes sum taken under the quota lock.
CREATE INDEX IF NOT EXISTS idx_attachments_pending_activity
    ON todo.attachments ((COALESCE(last_activity_at, created_at))) WHERE status = 'pending';
CREATE INDEX IF NOT EXISTS idx_attachments_pending_user
    ON todo.attachments (user_id) WHERE status = 'pending';

-- Blobs to remove, enqueued in the same tx that drops their attachment row.
CREATE TABLE IF NOT EXISTS todo.blob_gc (
    id            BIGSERIAL   PRIMARY KEY,
    object_key    TEXT        NOT NULL,
    upload_id     TEXT,
    claimed_until TIMESTAMPTZ,
    created_at    TIMESTAMPTZ NOT NULL DEFAULT now()
);
