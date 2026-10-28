-- +goose Up
-- An admin viewing the office as another account: the session is that
-- account's, impersonator_id the admin who opened it ('' = a normal login).
ALTER TABLE sessions ADD COLUMN impersonator_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE sessions DROP COLUMN impersonator_id;
