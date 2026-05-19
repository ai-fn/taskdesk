-- Enable pgvector extension (idempotent).
CREATE EXTENSION IF NOT EXISTS vector;

-- Store one embedding vector per task (1024-dim, voyage-4-lite).
CREATE TABLE todo.task_embeddings (
    task_id    TEXT        PRIMARY KEY REFERENCES todo.tasks(id) ON DELETE CASCADE,
    user_id    TEXT        NOT NULL,
    embedding  vector(1024) NOT NULL,
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);

CREATE INDEX idx_task_embeddings_user ON todo.task_embeddings(user_id);

-- HNSW index for fast approximate cosine-similarity search.
-- m=16 and ef_construction=64 are solid defaults for ≤1M vectors.
CREATE INDEX idx_task_embeddings_hnsw
    ON todo.task_embeddings
    USING hnsw (embedding vector_cosine_ops)
    WITH (m = 16, ef_construction = 64);
