-- +goose Up
-- The default admin (admin / admin) the first run makes: it has to set a real
-- email and password before it can do anything else.
ALTER TABLE users ADD COLUMN must_change INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE users DROP COLUMN must_change;
