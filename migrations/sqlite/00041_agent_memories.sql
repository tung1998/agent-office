-- +goose Up
-- Long-term memory (ADR-068): each agent's notes in a project, put in every
-- new conversation; snapshots kept before a compaction or a restore.
CREATE TABLE agent_memories (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    text       TEXT NOT NULL,
    source     TEXT NOT NULL DEFAULT 'person', -- person | agent | compact
    created_by TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);
CREATE INDEX agent_memories_agent ON agent_memories (project_id, agent_id, created_at);

CREATE TABLE agent_memory_revisions (
    id         TEXT PRIMARY KEY,
    project_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    agent_id   TEXT NOT NULL,
    items      TEXT NOT NULL CHECK (json_valid(items)),
    reason     TEXT NOT NULL DEFAULT '',
    created_at TEXT NOT NULL
);
CREATE INDEX agent_memory_revisions_agent ON agent_memory_revisions (project_id, agent_id, created_at);

-- +goose Down
DROP TABLE agent_memory_revisions;
DROP TABLE agent_memories;
