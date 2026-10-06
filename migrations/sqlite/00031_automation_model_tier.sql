-- +goose Up
-- An automation can ask for a cheaper model for its runs ('' = the agent's).
ALTER TABLE automations ADD COLUMN model_tier TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE automations DROP COLUMN model_tier;
