-- +goose Up
-- A chat thread between a person and one agent of a project.
CREATE TABLE conversations (
    id          TEXT PRIMARY KEY,
    project_id  TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    agent_id    TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_name  TEXT NOT NULL DEFAULT '',   -- kept if the agent is removed
    title       TEXT NOT NULL DEFAULT '',
    session_id  TEXT NOT NULL DEFAULT '',   -- runtime session to resume (Claude Code)
    runtime     TEXT NOT NULL DEFAULT '',   -- provider kind used for session_id
    created_by  TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
CREATE INDEX conversations_project ON conversations(project_id, updated_at);

CREATE TABLE messages (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    role            TEXT NOT NULL CHECK (role IN ('user', 'assistant', 'error')),
    content         TEXT NOT NULL DEFAULT '',
    tools           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tools)), -- tool calls made while answering
    run_id          TEXT REFERENCES runs(id) ON DELETE SET NULL,
    author          TEXT NOT NULL DEFAULT '',
    created_at      TEXT NOT NULL
);
CREATE INDEX messages_conversation ON messages(conversation_id, created_at);

-- Code changes an agent proposed; only applied after a person approves.
CREATE TABLE patches (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    diff            TEXT NOT NULL,
    files           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(files)),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'rejected', 'failed')),
    detail          TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX patches_conversation ON patches(conversation_id);

-- +goose Down
DROP TABLE patches;
DROP TABLE messages;
DROP TABLE conversations;
