CREATE TABLE todo.task_dependencies (
    task_id        TEXT        NOT NULL REFERENCES todo.tasks(id) ON DELETE CASCADE,
    depends_on_id  TEXT        NOT NULL REFERENCES todo.tasks(id) ON DELETE CASCADE,
    user_id        TEXT        NOT NULL,
    created_at     TIMESTAMPTZ NOT NULL DEFAULT now(),
    PRIMARY KEY (task_id, depends_on_id),
    CHECK (task_id <> depends_on_id)
);

CREATE INDEX idx_task_dependencies_task ON todo.task_dependencies(task_id);
CREATE INDEX idx_task_dependencies_dep  ON todo.task_dependencies(depends_on_id);
CREATE INDEX idx_task_dependencies_user ON todo.task_dependencies(user_id);
