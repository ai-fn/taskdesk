CREATE SCHEMA IF NOT EXISTS ai;

CREATE TABLE IF NOT EXISTS ai.daily_plans (
    plan_id      TEXT        NOT NULL PRIMARY KEY,
    user_id      TEXT        NOT NULL,
    plan_date    DATE        NOT NULL DEFAULT CURRENT_DATE,
    summary      TEXT        NOT NULL DEFAULT '',
    items_json   JSONB       NOT NULL DEFAULT '[]',
    generated_at TIMESTAMPTZ NOT NULL DEFAULT NOW(),
    UNIQUE (user_id, plan_date)
);

CREATE INDEX IF NOT EXISTS daily_plans_user_id_idx ON ai.daily_plans (user_id, generated_at DESC);
