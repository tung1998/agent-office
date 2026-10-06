-- +goose Up
-- An agent's avatar: a color and an icon, or a small uploaded image ('{}' =
-- the dashboard picks a random-looking one from its id).
ALTER TABLE agents ADD COLUMN avatar TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(avatar));

-- +goose Down
ALTER TABLE agents DROP COLUMN avatar;
