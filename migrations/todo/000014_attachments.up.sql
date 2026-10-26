CREATE TABLE todo.attachments (
    id           TEXT        PRIMARY KEY,
    user_id      TEXT        NOT NULL,
    task_id      TEXT        NOT NULL REFERENCES todo.tasks(id) ON DELETE CASCADE,
    filename     TEXT        NOT NULL,
    content_type TEXT        NOT NULL DEFAULT 'application/octet-stream',
    size_bytes   BIGINT      NOT NULL DEFAULT 0,
    object_key   TEXT        NOT NULL,
    status       TEXT        NOT NULL DEFAULT 'pending',   -- pending | committed
    scan_status  TEXT        NOT NULL DEFAULT 'clean',     -- clean | pending | quarantined (Plan 3)
    created_at   TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Keyset pagination of a task's committed attachments (newest first).
CREATE INDEX idx_attachments_task ON todo.attachments(user_id, task_id, created_at DESC, id DESC);
-- Reaper scan for abandoned pending uploads.
CREATE INDEX idx_attachments_pending ON todo.attachments(created_at) WHERE status = 'pending';

-- Denormalized counters so task cards never COUNT(*) over unbounded rows.
ALTER TABLE todo.tasks
    ADD COLUMN attachment_count INTEGER NOT NULL DEFAULT 0,
    ADD COLUMN attachment_bytes BIGINT  NOT NULL DEFAULT 0;

-- Per-user storage rollup for quota checks (one row per user, created on demand).
CREATE TABLE todo.user_storage (
    user_id    TEXT   PRIMARY KEY,
    used_bytes BIGINT NOT NULL DEFAULT 0
);
