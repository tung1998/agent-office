-- +goose Up
-- A personal token can expire: new ones last 90 days unless the person picks
-- otherwise. NULL is no expiry, which keeps the tokens made before this working.
ALTER TABLE user_tokens ADD COLUMN expires_at TEXT;

-- +goose Down
ALTER TABLE user_tokens DROP COLUMN expires_at;
