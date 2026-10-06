-- +goose Up
-- A chat that builds one automation, and the page context sent with a
-- message (ADR-042).
ALTER TABLE conversations ADD COLUMN purpose TEXT NOT NULL DEFAULT '';
ALTER TABLE conversations ADD COLUMN automation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX conversations_automation ON conversations(automation_id) WHERE automation_id != '';
ALTER TABLE messages ADD COLUMN context TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE messages DROP COLUMN context;
DROP INDEX conversations_automation;
ALTER TABLE conversations DROP COLUMN automation_id;
ALTER TABLE conversations DROP COLUMN purpose;
