-- +goose Up
-- One row per model call: tokens, cost and timing, for history and budgets.
CREATE TABLE runs (
    id            TEXT PRIMARY KEY,
    kind          TEXT NOT NULL,              -- provider_test | setup_propose | chat …
    project_id    TEXT REFERENCES repos(id) ON DELETE SET NULL,
    agent_id      TEXT REFERENCES agents(id) ON DELETE SET NULL,
    provider_id   TEXT REFERENCES providers(id) ON DELETE SET NULL,
    provider_name TEXT NOT NULL DEFAULT '',   -- kept when the connection is deleted
    model         TEXT NOT NULL DEFAULT '',
    status        TEXT NOT NULL CHECK (status IN ('ok', 'error', 'blocked')),
    input_tokens  INTEGER NOT NULL DEFAULT 0,
    output_tokens INTEGER NOT NULL DEFAULT 0,
    cost_usd      REAL,                       -- NULL when unknown
    cost_source   TEXT NOT NULL DEFAULT 'unknown' CHECK (cost_source IN ('provider', 'estimate', 'unknown')),
    duration_ms   INTEGER NOT NULL DEFAULT 0,
    error         TEXT NOT NULL DEFAULT '',
    actor         TEXT NOT NULL DEFAULT '',
    created_at    TEXT NOT NULL
);
CREATE INDEX runs_created ON runs(created_at);
CREATE INDEX runs_project ON runs(project_id, created_at);

-- Small key/value settings (JSON values): budgets, price overrides.
CREATE TABLE settings (
    key        TEXT PRIMARY KEY,
    value      TEXT NOT NULL CHECK (json_valid(value)),
    updated_at TEXT NOT NULL
);

-- +goose Down
DROP TABLE settings;
DROP TABLE runs;
