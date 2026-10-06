-- +goose Up
-- Two-way chat channels (ADR-048): a Telegram or Discord bot answered by an
-- agent of the project; each outside chat is one conversation.
CREATE TABLE channels (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    kind            TEXT NOT NULL CHECK (kind IN ('telegram', 'discord')),
    name            TEXT NOT NULL DEFAULT '',
    token_enc       TEXT NOT NULL DEFAULT '',
    agent_id        TEXT NOT NULL DEFAULT '',
    mode            TEXT NOT NULL DEFAULT 'read',
    enabled         INTEGER NOT NULL DEFAULT 1,
    allow           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(allow)),
    scope           TEXT NOT NULL DEFAULT '',
    filter_enabled  INTEGER NOT NULL DEFAULT 0,
    refusal         TEXT NOT NULL DEFAULT '',
    bot_name        TEXT NOT NULL DEFAULT '',
    last_error      TEXT NOT NULL DEFAULT '',
    last_message_at TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);
CREATE INDEX channels_project ON channels(project_id);
CREATE TABLE channel_threads (
    channel_id      TEXT NOT NULL REFERENCES channels(id) ON DELETE CASCADE,
    chat_id         TEXT NOT NULL,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    PRIMARY KEY (channel_id, chat_id)
);

-- +goose Down
DROP TABLE channel_threads;
DROP TABLE channels;
