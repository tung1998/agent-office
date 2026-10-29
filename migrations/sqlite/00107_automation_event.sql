-- An automation can run when something in its project ends (source "event",
-- the event in config.event, ADR-243). SQLite cannot change a CHECK
-- constraint, so the table is rebuilt with foreign keys off.

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE automations_new (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    source          TEXT NOT NULL CHECK (source IN ('schedule','webhook','telegram','discord','event')),
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    action          TEXT NOT NULL CHECK (action IN ('chat','task','script','workflow')),
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
    escalate        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(escalate)),
    model_tier      TEXT NOT NULL DEFAULT '',
    permission_mode TEXT NOT NULL DEFAULT 'agent',
    override_full_access BOOLEAN NOT NULL DEFAULT 0,
    override_admin_by    TEXT NOT NULL DEFAULT '',
    override_extra_dirs  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(override_extra_dirs))
);
INSERT INTO automations_new (id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits, failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at, script, escalate, model_tier, permission_mode, override_full_access, override_admin_by, override_extra_dirs)
    SELECT id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits, failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at, script, escalate, model_tier, permission_mode, override_full_access, override_admin_by, override_extra_dirs FROM automations;
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
CREATE INDEX automations_project ON automations(project_id);
CREATE INDEX automations_due ON automations(enabled, source, next_run_at);

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

CREATE TABLE automations_new (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name            TEXT NOT NULL,
    enabled         INTEGER NOT NULL DEFAULT 1,
    source          TEXT NOT NULL CHECK (source IN ('schedule','webhook','telegram','discord')),
    config          TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(config)),
    action          TEXT NOT NULL CHECK (action IN ('chat','task','script','workflow')),
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
    escalate        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(escalate)),
    model_tier      TEXT NOT NULL DEFAULT '',
    permission_mode TEXT NOT NULL DEFAULT 'agent',
    override_full_access BOOLEAN NOT NULL DEFAULT 0,
    override_admin_by    TEXT NOT NULL DEFAULT '',
    override_extra_dirs  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(override_extra_dirs))
);
INSERT INTO automations_new (id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits, failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at, script, escalate, model_tier, permission_mode, override_full_access, override_admin_by, override_extra_dirs)
    SELECT id, project_id, name, enabled, source, config, action, agent_id, prompt, edit_mode, keep_context, limits, failures, disabled_code, disabled_reason, last_run_at, next_run_at, created_by, created_at, updated_at, script, escalate, model_tier, permission_mode, override_full_access, override_admin_by, override_extra_dirs FROM automations WHERE source != 'event';
DROP TABLE automations;
ALTER TABLE automations_new RENAME TO automations;
CREATE INDEX automations_project ON automations(project_id);
CREATE INDEX automations_due ON automations(enabled, source, next_run_at);

PRAGMA foreign_keys = ON;
