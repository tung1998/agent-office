-- +goose Up
-- The permission mode chosen for a chat or a task: a ceiling on what its
-- agents may do on their own (see internal/perm). "propose" = ask first.
ALTER TABLE conversations ADD COLUMN mode TEXT NOT NULL DEFAULT 'propose';
ALTER TABLE tasks ADD COLUMN mode_level TEXT NOT NULL DEFAULT 'propose';

-- +goose Down
ALTER TABLE tasks DROP COLUMN mode_level;
ALTER TABLE conversations DROP COLUMN mode;
