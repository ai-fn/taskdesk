DROP INDEX IF EXISTS todo.idx_tasks_parent;
ALTER TABLE todo.tasks DROP COLUMN IF EXISTS parent_task_id;
