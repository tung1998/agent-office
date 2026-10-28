-- +goose Up
-- Passkeys (WebAuthn) an account signs in with besides its password (ADR-226).
-- credential is the library's credential record as JSON (public key, sign count).
CREATE TABLE passkeys (
    id            TEXT PRIMARY KEY,
    user_id       TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    name          TEXT NOT NULL DEFAULT '',
    credential_id TEXT NOT NULL UNIQUE,
    credential    TEXT NOT NULL,
    created_at    TEXT NOT NULL,
    last_used_at  TEXT
);
CREATE INDEX passkeys_user ON passkeys(user_id);

-- +goose Down
DROP TABLE passkeys;
