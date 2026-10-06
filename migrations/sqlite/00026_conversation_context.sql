-- +goose Up
-- How full the model's context was after the last answer (shown in the chat).
ALTER TABLE conversations ADD COLUMN context_tokens INTEGER NOT NULL DEFAULT 0;
ALTER TABLE conversations ADD COLUMN context_window INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE conversations DROP COLUMN context_window;
ALTER TABLE conversations DROP COLUMN context_tokens;
