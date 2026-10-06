-- +goose Up
-- One building chat per automation (two tabs must not make two, ADR-042).
DROP INDEX conversations_automation;
CREATE UNIQUE INDEX conversations_automation ON conversations(automation_id) WHERE automation_id != '';

-- +goose Down
DROP INDEX conversations_automation;
CREATE INDEX conversations_automation ON conversations(automation_id) WHERE automation_id != '';
