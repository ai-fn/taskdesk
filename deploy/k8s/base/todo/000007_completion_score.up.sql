ALTER TABLE todo.tasks
    ADD COLUMN IF NOT EXISTS completion_score   SMALLINT,  -- 0–100; NULL = not yet scored
    ADD COLUMN IF NOT EXISTS completion_insight TEXT;      -- Claude's one-liner explanation
