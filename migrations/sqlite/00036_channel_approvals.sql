-- +goose Up
-- Deciding proposals from the chat (ADR-054): who may approve there, and
-- whether a new chat asks first (ask) or approves what the agent proposes (direct).
ALTER TABLE channels ADD COLUMN approvers TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(approvers));
ALTER TABLE channels ADD COLUMN approval TEXT NOT NULL DEFAULT 'ask';

-- +goose Down
ALTER TABLE channels DROP COLUMN approval;
ALTER TABLE channels DROP COLUMN approvers;
