-- +goose Up
-- Job mode in the chat was dropped (ADR-056): tasks are not tied to a chat.
DROP INDEX IF EXISTS tasks_conversation;
ALTER TABLE tasks DROP COLUMN conversation_id;

-- +goose Down
ALTER TABLE tasks ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
