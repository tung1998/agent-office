-- +goose Up
-- Health checks of a project (like an uptime monitor). Rule-based checks are
-- free; ai_enabled asks an agent to analyse when a monitor goes down.
CREATE TABLE monitors (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    type            TEXT NOT NULL CHECK (type IN ('http', 'tcp', 'heartbeat', 'process', 'container')),
    target          TEXT NOT NULL DEFAULT '',  -- url | host:port | process id | compose service
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)), -- expect_status, keyword, timeout_ms, file
    interval_s      INTEGER NOT NULL DEFAULT 60,
    enabled         INTEGER NOT NULL DEFAULT 1,
    ai_enabled      INTEGER NOT NULL DEFAULT 0,
    ai_budget_usd   REAL NOT NULL DEFAULT 0.5,  -- per day, for this monitor's analyses
    token           TEXT NOT NULL DEFAULT '',   -- heartbeat push token
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'up', 'down')),
    fails           INTEGER NOT NULL DEFAULT 0, -- consecutive failed checks
    last_checked_at TEXT,
    last_change_at  TEXT,
    last_latency_ms INTEGER NOT NULL DEFAULT 0,
    last_message    TEXT NOT NULL DEFAULT '',
    last_ping_at    TEXT,                       -- heartbeat
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    UNIQUE (project_id, name)
);
CREATE UNIQUE INDEX monitors_token ON monitors(token) WHERE token != '';

CREATE TABLE monitor_checks (
    monitor_id  TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    at          TEXT NOT NULL,
    ok          INTEGER NOT NULL,
    latency_ms  INTEGER NOT NULL DEFAULT 0,
    message     TEXT NOT NULL DEFAULT ''
);
CREATE INDEX monitor_checks_at ON monitor_checks(monitor_id, at);

CREATE TABLE monitor_events (
    id           TEXT PRIMARY KEY,
    monitor_id   TEXT NOT NULL REFERENCES monitors(id) ON DELETE CASCADE,
    project_id   TEXT NOT NULL,
    kind         TEXT NOT NULL CHECK (kind IN ('up', 'down')),
    message      TEXT NOT NULL DEFAULT '',
    analysis     TEXT NOT NULL DEFAULT '',      -- AI analysis (down events)
    analysis_status TEXT NOT NULL DEFAULT '',   -- '' | running | done | skipped | failed
    cost_usd     REAL NOT NULL DEFAULT 0,
    at           TEXT NOT NULL
);
CREATE INDEX monitor_events_at ON monitor_events(project_id, at);
CREATE INDEX monitor_events_monitor ON monitor_events(monitor_id, at);

-- +goose Down
DROP TABLE monitor_events;
DROP TABLE monitor_checks;
DROP TABLE monitors;
