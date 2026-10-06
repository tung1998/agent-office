-- +goose Up
-- The worktree a diff was taken from (ADR-044: agents of a chat have their
-- own), so it is accepted in that tree and replaces only that tree's diff.
ALTER TABLE patches ADD COLUMN tree TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE patches DROP COLUMN tree;
