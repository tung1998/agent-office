-- +goose Up
-- Burn (spec 2026-10-01-burn-design): an agent that runs on its own in a
-- project, finding work and doing it, each piece in its own worktree.
CREATE TABLE burn_sessions (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL UNIQUE REFERENCES repos(id) ON DELETE CASCADE,
    conversation_id TEXT NOT NULL DEFAULT '',
    agent_id        TEXT NOT NULL DEFAULT '',
    model_tier      TEXT NOT NULL DEFAULT 'balanced',
    max_subagents   INTEGER NOT NULL DEFAULT 2,
    result_mode     TEXT NOT NULL DEFAULT 'branch', -- branch | patch
    focus           TEXT NOT NULL DEFAULT '',
    ends_at         TEXT,                           -- null: no stop time
    state           TEXT NOT NULL DEFAULT 'stopped', -- running | stopped | waiting_limit
    waiting_until   TEXT,
    started_by      TEXT NOT NULL DEFAULT '',
    started_at      TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE burn_items (
    id          TEXT PRIMARY KEY,
    session_id  TEXT NOT NULL REFERENCES burn_sessions(id) ON DELETE CASCADE,
    title       TEXT NOT NULL,
    kind        TEXT NOT NULL DEFAULT 'upgrade', -- unfinished | upgrade | bug
    detail      TEXT NOT NULL DEFAULT '',
    status      TEXT NOT NULL DEFAULT 'found',   -- found | queued | doing | paused | done | failed | skipped
    priority    INTEGER NOT NULL DEFAULT 0,
    branch      TEXT NOT NULL DEFAULT '',
    worktree    TEXT NOT NULL DEFAULT '',
    summary     TEXT NOT NULL DEFAULT '',
    attempts    INTEGER NOT NULL DEFAULT 0,
    subagents   INTEGER NOT NULL DEFAULT 0,
    cost_usd    REAL NOT NULL DEFAULT 0,
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX burn_items_session ON burn_items (session_id, status, priority, created_at);

-- +goose Down
DROP TABLE burn_items;
DROP TABLE burn_sessions;
