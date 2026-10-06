-- +goose Up
-- An agent can be paused from the agent list: a paused agent is left out of
-- chat and anyone calling it gets a short notice instead of an AI run.
ALTER TABLE agents ADD COLUMN enabled INTEGER NOT NULL DEFAULT 1;

-- +goose Down
ALTER TABLE agents DROP COLUMN enabled;
