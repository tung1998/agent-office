-- +goose Up
-- Automations that run code, and the jobs they start (ADR-041). SQLite cannot
-- change a CHECK, so both tables are rebuilt.
CREATE TABLE jobs_new (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('chat_turn','task','script')),
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
    finished_at     TEXT,
    output          TEXT NOT NULL DEFAULT '',
    exit_code       INTEGER,
    parent_job_id   TEXT NOT NULL DEFAULT ''
);
INSERT INTO jobs_new (id, project_id, kind, origin, origin_id, trigger, created_by, conversation_id, message_id, task_id, status, error, error_code,
    agent_id, title, cost_usd, input_tokens, output_tokens, duration_ms, dedupe_key, debounce_key, debounce_until, payload, reply,
    next_attempt_at, created_at, started_at, finished_at)
SELECT id, project_id, kind, origin, origin_id, trigger, created_by, conversation_id, message_id, task_id, status, error, error_code,
    agent_id, title, cost_usd, input_tokens, output_tokens, duration_ms, dedupe_key, debounce_key, debounce_until, payload, reply,
    next_attempt_at, created_at, started_at, finished_at FROM jobs;
DROP TABLE jobs;
ALTER TABLE jobs_new RENAME TO jobs;
CREATE INDEX jobs_project ON jobs(project_id, created_at);
CREATE INDEX jobs_queue ON jobs(status, next_attempt_at);
CREATE INDEX jobs_origin ON jobs(origin, origin_id, created_at);
CREATE INDEX jobs_kind ON jobs(kind, created_at);
CREATE INDEX jobs_agent ON jobs(agent_id, created_at);
CREATE INDEX jobs_parent ON jobs(parent_job_id) WHERE parent_job_id != '';
CREATE UNIQUE INDEX jobs_dedupe ON jobs(origin_id, dedupe_key) WHERE dedupe_key != '';

CREATE TABLE automations_new (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    source          TEXT NOT NULL CHECK (source IN ('schedule','webhook','telegram','discord')),
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    action          TEXT NOT NULL CHECK (action IN ('chat','task','script')),
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
    updated_at      TEXT NOT NULL,
    script          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(script)),
    escalate        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(escalate))
);
INSERT INTO automations_new (id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits,
    failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at)
SELECT id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits,
    failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at FROM automations;
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
CREATE INDEX automations_project ON automations(project_id);
CREATE INDEX automations_due ON automations(enabled, source, next_run_at);

-- +goose Down
SELECT 1;
