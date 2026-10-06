-- +goose Up
-- A task can go to one agent (a person's daily job) instead of the team.
ALTER TABLE tasks ADD COLUMN assignee_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tasks DROP COLUMN assignee_id;
