-- +goose Up
-- Every run (a chat answer, a task, an automation's turn) and the queue (ADR-040).
CREATE TABLE jobs (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('chat_turn','task')),
    origin          TEXT NOT NULL CHECK (origin IN ('user','automation','monitor','retry')),
    origin_id       TEXT NOT NULL DEFAULT '',
    trigger         TEXT NOT NULL DEFAULT 'ui',
    created_by      TEXT NOT NULL DEFAULT '',
    conversation_id TEXT,
    message_id      TEXT,
    task_id         TEXT,
    status          TEXT NOT NULL CHECK (status IN ('pending','running','done','failed','cancelled','skipped','needs_input')),
    error           TEXT NOT NULL DEFAULT '',
    error_code      TEXT NOT NULL DEFAULT '',
    agent_id        TEXT NOT NULL DEFAULT '',
    title           TEXT NOT NULL DEFAULT '',
    cost_usd        REAL NOT NULL DEFAULT 0,
    input_tokens    INTEGER NOT NULL DEFAULT 0,
    output_tokens   INTEGER NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    dedupe_key      TEXT NOT NULL DEFAULT '',
    debounce_key    TEXT NOT NULL DEFAULT '',
    debounce_until  TEXT,
    payload         TEXT NOT NULL DEFAULT '',
    reply           TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(reply)),
    next_attempt_at TEXT,
    created_at      TEXT NOT NULL,
    started_at      TEXT,
    finished_at     TEXT
);
CREATE INDEX jobs_project ON jobs(project_id, created_at);
CREATE INDEX jobs_queue ON jobs(status, next_attempt_at);
CREATE INDEX jobs_origin ON jobs(origin, origin_id, created_at);
CREATE INDEX jobs_kind ON jobs(kind, created_at);
CREATE INDEX jobs_agent ON jobs(agent_id, created_at);
CREATE UNIQUE INDEX jobs_dedupe ON jobs(origin_id, dedupe_key) WHERE dedupe_key != '';

CREATE TABLE automations (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    source          TEXT NOT NULL CHECK (source IN ('schedule','webhook','telegram','discord')),
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    action          TEXT NOT NULL CHECK (action IN ('chat','task')),
    agent_id        TEXT NOT NULL DEFAULT '',
    prompt          TEXT NOT NULL DEFAULT '',
    edit_mode       TEXT NOT NULL DEFAULT 'worktree',
    keep_context    INTEGER NOT NULL DEFAULT 0,
    limits          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(limits)),
    failures        INTEGER NOT NULL DEFAULT 0,
    disabled_code   TEXT NOT NULL DEFAULT '',
    disabled_reason TEXT NOT NULL DEFAULT '',
    last_run_at     TEXT,
    next_run_at     TEXT,
    created_by      TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX automations_project ON automations(project_id);
CREATE INDEX automations_due ON automations(enabled, source, next_run_at);

ALTER TABLE runs ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
CREATE INDEX runs_job ON runs(job_id) WHERE job_id != '';

-- +goose Down
DROP INDEX runs_job;
ALTER TABLE runs DROP COLUMN job_id;
DROP TABLE automations;
DROP TABLE jobs;
