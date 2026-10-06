-- +goose Up
-- The connections an agent falls back to, in order, when its own one fails or
-- is over its limit (JSON array of provider ids; a deleted one is skipped).
ALTER TABLE agents ADD COLUMN fallback_provider_ids TEXT NOT NULL DEFAULT '[]';

-- +goose Down
ALTER TABLE agents DROP COLUMN fallback_provider_ids;
