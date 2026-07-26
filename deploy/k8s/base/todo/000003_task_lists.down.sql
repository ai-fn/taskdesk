ALTER TABLE todo.tasks
    DROP COLUMN IF EXISTS list_id,
    DROP COLUMN IF EXISTS flagged,
    DROP COLUMN IF EXISTS deleted_at;

DROP TABLE IF EXISTS todo.lists;
