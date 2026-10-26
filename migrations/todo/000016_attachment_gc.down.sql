DROP TABLE IF EXISTS todo.blob_gc;
DROP INDEX IF EXISTS todo.idx_attachments_pending_user;
DROP INDEX IF EXISTS todo.idx_attachments_pending_activity;
ALTER TABLE todo.attachments DROP COLUMN IF EXISTS last_activity_at;
