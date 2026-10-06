-- +goose Up
-- Việc is gone (ADR-057): its old rows go too — the steps, diffs, cards and
-- talks of each task, then the tasks and their jobs. Chats, their diffs and
-- the cost records (runs) stay.
DELETE FROM task_steps;
DELETE FROM patches WHERE task_id IS NOT NULL AND task_id <> '' AND COALESCE(conversation_id, '') = '';
UPDATE patches SET task_id = NULL WHERE task_id IS NOT NULL AND task_id <> '';
DELETE FROM actions WHERE task_id IS NOT NULL AND task_id <> '' AND COALESCE(conversation_id, '') = '';
UPDATE actions SET task_id = NULL WHERE task_id IS NOT NULL AND task_id <> '';
DELETE FROM conversations WHERE task_id IS NOT NULL;
DELETE FROM jobs WHERE kind = 'task';
DELETE FROM tasks;
-- a bot made and dropped in the same save once left a "channel.create" behind
DELETE FROM audit_log WHERE action = 'channel.create' AND resource_id NOT IN (SELECT id FROM channels)
    AND resource_id NOT IN (SELECT resource_id FROM audit_log WHERE action = 'channel.delete');

-- +goose Down
SELECT 1;
