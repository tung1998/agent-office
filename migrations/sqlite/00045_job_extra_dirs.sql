-- +goose Up
-- ADR-074 security fix: extra read dirs are computed once per job, same as
-- full_access — an automation's override replaces the agent's own, it never
-- adds to it.
ALTER TABLE jobs ADD COLUMN extra_dirs TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE jobs DROP COLUMN extra_dirs;
