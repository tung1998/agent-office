-- +goose Up
-- How hard the model thinks (low | medium | high | xhigh | max; '' = the
-- CLI's own): an agent's default, and a chat's own choice over it.
ALTER TABLE agents ADD COLUMN effort TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN effort TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE conversations DROP COLUMN effort;
ALTER TABLE agents DROP COLUMN effort;
