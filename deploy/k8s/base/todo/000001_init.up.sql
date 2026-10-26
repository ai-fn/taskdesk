CREATE SCHEMA IF NOT EXISTS todo;

-- tags is a text array; simple and fast for the scale of a todo app.
CREATE TABLE todo.tasks (
    id          TEXT        PRIMARY KEY,
    user_id     TEXT        NOT NULL,
    title       TEXT        NOT NULL,
    description TEXT        NOT NULL DEFAULT '',
    priority    SMALLINT    NOT NULL DEFAULT 0,
    status      SMALLINT    NOT NULL DEFAULT 1,
    tags        TEXT[]      NOT NULL DEFAULT '{}',
    due_at      TIMESTAMPTZ,
    created_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now()
);

-- Primary query patterns: by user + status filter, paginated by created_at DESC.
CREATE INDEX idx_tasks_user_id           ON todo.tasks(user_id);
CREATE INDEX idx_tasks_user_status       ON todo.tasks(user_id, status);
CREATE INDEX idx_tasks_user_created_at   ON todo.tasks(user_id, created_at DESC);
