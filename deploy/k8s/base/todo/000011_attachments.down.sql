DROP TABLE IF EXISTS todo.user_storage;
ALTER TABLE todo.tasks DROP COLUMN IF EXISTS attachment_bytes;
ALTER TABLE todo.tasks DROP COLUMN IF EXISTS attachment_count;
DROP TABLE IF EXISTS todo.attachments;
