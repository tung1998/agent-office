-- +goose Up
-- Files (images, PDFs, text) attached to a chat message or a task. The files
-- live under <office>/attachments; rows keep only the references.
ALTER TABLE messages ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attachments));
ALTER TABLE tasks ADD COLUMN attachments TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attachments));

-- +goose Down
ALTER TABLE tasks DROP COLUMN attachments;
ALTER TABLE messages DROP COLUMN attachments;
