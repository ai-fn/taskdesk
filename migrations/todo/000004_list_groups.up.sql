CREATE TABLE todo.list_groups (
    id         TEXT        PRIMARY KEY,
    user_id    TEXT        NOT NULL,
    name       TEXT        NOT NULL,
    position   INTEGER     NOT NULL DEFAULT 0,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX idx_list_groups_user ON todo.list_groups(user_id);

ALTER TABLE todo.lists
    ADD COLUMN group_id TEXT REFERENCES todo.list_groups(id) ON DELETE SET NULL;

-- Seed "My lists" group for every existing user that has lists, then assign their lists to it.
INSERT INTO todo.list_groups (id, user_id, name, position)
SELECT gen_random_uuid()::text, user_id, 'My lists', 0
FROM (SELECT DISTINCT user_id FROM todo.lists) u;

UPDATE todo.lists l
SET group_id = (
    SELECT lg.id FROM todo.list_groups lg
    WHERE lg.user_id = l.user_id
    LIMIT 1
);
