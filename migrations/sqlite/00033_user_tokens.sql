-- +goose Up
-- Personal tokens for a person's own Claude Code CLI (ADR-047): the office
-- tools over MCP in office scope. Only the hash is kept.
CREATE TABLE user_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked      INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX user_tokens_user ON user_tokens(user_id);

-- +goose Down
DROP TABLE user_tokens;
