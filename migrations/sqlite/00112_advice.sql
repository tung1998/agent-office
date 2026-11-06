-- An advisor's second opinion for an agent stuck on the same failure
-- (ADR-262): one row per question, its answer, who answered and what it cost.
-- trigger: repeat (a failure repeated in a run) | done (the review of such a
-- run's answer before it ends). status: answered | failed | skipped.

-- +goose Up
CREATE TABLE advice (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    job_id          TEXT NOT NULL DEFAULT '',
    message_id      TEXT NOT NULL DEFAULT '',   -- the answer of the run it went to, once there
    agent_id        TEXT NOT NULL DEFAULT '',   -- the stuck agent
    agent_name      TEXT NOT NULL DEFAULT '',
    advisor_id      TEXT NOT NULL DEFAULT '',
    advisor_name    TEXT NOT NULL DEFAULT '',
    provider        TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    trigger         TEXT NOT NULL,
    command         TEXT NOT NULL DEFAULT '',
    error           TEXT NOT NULL DEFAULT '',
    answer          TEXT NOT NULL DEFAULT '',
    objection       INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL,
    reason          TEXT NOT NULL DEFAULT '',   -- skipped/failed: why
    cost_usd        REAL,
    created_at      TEXT NOT NULL
);
CREATE INDEX advice_conversation ON advice(conversation_id, created_at);
CREATE INDEX advice_job ON advice(job_id);
CREATE INDEX advice_created ON advice(created_at);

-- +goose Down
DROP TABLE advice;
