-- Enable pg_trgm for trigram similarity search (used by free-tier task search).
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- GIN index for trigram similarity — accelerates the % operator used in
-- SearchTasksByText. Postgres uses this when evaluating col % $query.
CREATE INDEX idx_tasks_text_trgm
  ON todo.tasks USING GIN ((title || ' ' || description) gin_trgm_ops);

-- GIN index for full-text search — accelerates @@ plainto_tsquery.
CREATE INDEX idx_tasks_text_fts
  ON todo.tasks USING GIN (to_tsvector('simple', title || ' ' || description));
