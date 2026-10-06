-- +goose Up
-- The line on top of a bot's answers: a template of {agent} {project}
-- {branch} ('' = the default, '-' = none).
ALTER TABLE channels ADD COLUMN header TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE channels DROP COLUMN header;
