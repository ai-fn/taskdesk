ALTER TABLE todo.tasks
  ADD COLUMN IF NOT EXISTS parent_task_id TEXT REFERENCES todo.tasks(id) ON DELETE CASCADE;

CREATE INDEX IF NOT EXISTS idx_tasks_parent ON todo.tasks(parent_task_id)
  WHERE parent_task_id IS NOT NULL;
