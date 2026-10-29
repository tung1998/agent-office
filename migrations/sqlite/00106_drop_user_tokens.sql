-- +goose Up
-- The personal tokens of a person's own Claude Code CLI are gone (ADR-241).
DROP TABLE user_tokens;

-- +goose Down
CREATE TABLE user_tokens (
    id           TEXT PRIMARY KEY,
    user_id      TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name         TEXT NOT NULL DEFAULT '',
    token_hash   TEXT NOT NULL UNIQUE,
    created_at   TEXT NOT NULL,
    last_used_at TEXT,
    revoked      INTEGER NOT NULL DEFAULT 0,
    expires_at   TEXT
);
CREATE INDEX user_tokens_user ON user_tokens(user_id);
