-- Enable pg_trgm for trigram similarity search (used by free-tier task search).
-- NOTE: CREATE EXTENSION requires a superuser (or a role with CREATE privilege on
-- the database). In dev the migration runs as POSTGRES_USER=postgres (superuser)
-- so this works out of the box. In a hardened production environment where the
-- migration role is least-privilege, a DBA must run this once before deploying:
--   psql -U superuser -d <db> -c "CREATE EXTENSION IF NOT EXISTS pg_trgm;"
CREATE EXTENSION IF NOT EXISTS pg_trgm;

-- GIN index for trigram similarity — accelerates the % operator used in
-- SearchTasksByText. Postgres uses this when evaluating col % $query.
CREATE INDEX idx_tasks_text_trgm
  ON todo.tasks USING GIN ((title || ' ' || description) gin_trgm_ops);

-- GIN index for full-text search — accelerates @@ plainto_tsquery.
CREATE INDEX idx_tasks_text_fts
  ON todo.tasks USING GIN (to_tsvector('simple', title || ' ' || description));
