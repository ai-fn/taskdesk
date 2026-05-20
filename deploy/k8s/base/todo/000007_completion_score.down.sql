ALTER TABLE todo.tasks
    DROP COLUMN IF EXISTS completion_score,
    DROP COLUMN IF EXISTS completion_insight;
