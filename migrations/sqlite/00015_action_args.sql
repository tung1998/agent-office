-- +goose Up
-- Extra arguments of an action as JSON (git: commit message, files, branch).
ALTER TABLE actions ADD COLUMN args TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(args));

-- +goose Down
ALTER TABLE actions DROP COLUMN args;
