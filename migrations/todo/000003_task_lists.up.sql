CREATE TABLE todo.lists (
    id         TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    color      TEXT        NOT NULL DEFAULT '#007AFF',
    position   INTEGER     NOT NULL DEFAULT 0,
    is_hidden  BOOLEAN     NOT NULL DEFAULT FALSE,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_lists_user_id ON todo.lists(user_id);

ALTER TABLE todo.tasks
    ADD COLUMN list_id    TEXT        REFERENCES todo.lists(id) ON DELETE SET NULL,
    ADD COLUMN flagged    BOOLEAN     NOT NULL DEFAULT FALSE,
    ADD COLUMN deleted_at TIMESTAMPTZ;

CREATE INDEX idx_tasks_user_list ON todo.tasks(user_id, list_id);
CREATE INDEX idx_tasks_flagged   ON todo.tasks(user_id, flagged) WHERE flagged = TRUE;
CREATE INDEX idx_tasks_deleted   ON todo.tasks(user_id, deleted_at) WHERE deleted_at IS NOT NULL;
