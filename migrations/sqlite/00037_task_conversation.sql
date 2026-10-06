-- +goose Up
-- A job started from a chat (ADR-055): the chat it belongs to — its steps show
-- there, what the person writes there steers it, its result is answered there.
ALTER TABLE tasks ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX tasks_conversation ON tasks(conversation_id) WHERE conversation_id != '';

-- +goose Down
DROP INDEX tasks_conversation;
ALTER TABLE tasks DROP COLUMN conversation_id;
