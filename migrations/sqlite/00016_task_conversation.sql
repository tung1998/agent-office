-- +goose Up
-- A follow-up conversation with the lead about one finished task.
ALTER TABLE conversations ADD COLUMN task_id TEXT REFERENCES tasks(id) ON DELETE CASCADE;
CREATE UNIQUE INDEX conversations_task ON conversations(task_id) WHERE task_id IS NOT NULL;

-- +goose Down
DROP INDEX conversations_task;
ALTER TABLE conversations DROP COLUMN task_id;
