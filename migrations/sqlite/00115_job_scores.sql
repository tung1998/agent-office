-- Model/effort scoring (ADR-267): one score per finished job, what it ran on
-- (agent, connection, model, effort actually sent) and how it went (Q 0..100
-- from the parts, NULL = none applies / not scored), re-scored on later events
-- for 7 days, then frozen. runs.effort: the effort a call was sent with ('' =
-- the CLI's own). conversation_agents.llm_model: a chat's own model for one of
-- its agents ('' = the agent's).

-- +goose Up
CREATE TABLE job_scores (
    job_id          TEXT PRIMARY KEY REFERENCES jobs(id) ON DELETE CASCADE,
    project_id      TEXT NOT NULL,
    conversation_id TEXT NOT NULL DEFAULT '',
    message_id      TEXT NOT NULL DEFAULT '',
    agent_id        TEXT NOT NULL DEFAULT '',
    provider_id     TEXT NOT NULL DEFAULT '',
    model           TEXT NOT NULL DEFAULT '',
    effort          TEXT NOT NULL DEFAULT '',
    task_kind       TEXT NOT NULL DEFAULT '',
    difficulty      TEXT NOT NULL DEFAULT '',
    label_manual    INTEGER NOT NULL DEFAULT 0,
    status          TEXT NOT NULL DEFAULT '',
    q               INTEGER,
    parts           TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(parts)),
    feedback        INTEGER NOT NULL DEFAULT 0 CHECK (feedback IN (-1, 0, 1)),
    cost_usd        REAL NOT NULL DEFAULT 0,
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    finished_at     TEXT NOT NULL,
    updated_at      TEXT NOT NULL,
    frozen          INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX job_scores_project ON job_scores(project_id, finished_at);
CREATE INDEX job_scores_message ON job_scores(message_id) WHERE message_id != '';
CREATE INDEX job_scores_open ON job_scores(finished_at) WHERE frozen = 0;
ALTER TABLE runs ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE conversation_agents ADD COLUMN llm_model TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversation_agents DROP COLUMN llm_model;
ALTER TABLE runs DROP COLUMN effort;
DROP TABLE job_scores;
