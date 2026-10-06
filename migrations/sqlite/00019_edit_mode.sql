-- +goose Up
-- Where each chat/task changes code: its own worktree (default) or the
-- project folder like the CLI (ADR-037); it was a project setting before.
ALTER TABLE conversations ADD COLUMN edit_mode TEXT NOT NULL DEFAULT 'worktree';
ALTER TABLE tasks ADD COLUMN edit_mode TEXT NOT NULL DEFAULT 'worktree';

-- +goose Down
ALTER TABLE tasks DROP COLUMN edit_mode;
ALTER TABLE conversations DROP COLUMN edit_mode;
