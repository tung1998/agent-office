-- +goose Up
-- A bot's chats run with the answering agent's own rights (ADR-049): the ones
-- made while they were held to read only get the agent's rights back.
UPDATE conversations SET mode = 'operate' WHERE purpose = 'channel' AND mode = 'read';

-- +goose Down
SELECT 1;
