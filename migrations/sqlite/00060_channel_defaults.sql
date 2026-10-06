-- +goose Up
-- A bot's default setup for its commands (what they do: agent, tags, content,
-- permission, limits), as JSON; a command without its own setup takes it.
ALTER TABLE channels ADD COLUMN defaults TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE channels DROP COLUMN defaults;
