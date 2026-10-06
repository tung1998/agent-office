-- +goose Up
-- What a bot shows of a run: its answer only ('' = answer) or its steps too,
-- kept as the CLI shows them ('steps'). A command may override it (its config).
ALTER TABLE channels ADD COLUMN reply_mode TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE channels DROP COLUMN reply_mode;
