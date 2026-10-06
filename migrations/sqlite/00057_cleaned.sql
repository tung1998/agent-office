-- +goose Up
-- Data cleanup: a chat or a finished task whose content was taken out
-- ('content') or put in a few lines ('summary'); its title stays, it takes no
-- more messages. '' = as it was.
ALTER TABLE conversations ADD COLUMN cleaned TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN cleaned_at TEXT;
ALTER TABLE tasks ADD COLUMN cleaned TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE tasks DROP COLUMN cleaned;
ALTER TABLE conversations DROP COLUMN cleaned_at;
ALTER TABLE conversations DROP COLUMN cleaned;
