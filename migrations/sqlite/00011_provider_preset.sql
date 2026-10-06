-- +goose Up
-- Which catalog entry a connection was made from (openrouter, deepseek…), for
-- its icon and key link. Empty for connections made by hand.
ALTER TABLE providers ADD COLUMN preset TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE providers DROP COLUMN preset;
