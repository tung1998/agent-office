-- +goose Up
-- Where a diff comes from: '' = written by the agent, 'worktree' = the
-- agent's edits in its own git worktree (see ADR-037).
ALTER TABLE patches ADD COLUMN origin TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE patches DROP COLUMN origin;
