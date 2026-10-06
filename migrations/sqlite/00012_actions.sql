-- +goose Up
-- Operations an agent proposed (run/restart/stop a process or a compose
-- service). Nothing runs until a person approves, like code patches.
CREATE TABLE actions (
    id              TEXT PRIMARY KEY,
    project_id      TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      TEXT,
    task_id         TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    run_ref         TEXT NOT NULL DEFAULT '',   -- the agent run that proposed it
    kind            TEXT NOT NULL,              -- run_process | restart_process | stop_process | start_container | restart_container | stop_container
    target          TEXT NOT NULL,              -- process name or compose service
    target_id       TEXT NOT NULL DEFAULT '',   -- process id
    reason          TEXT NOT NULL DEFAULT '',
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'done', 'failed', 'rejected')),
    detail          TEXT NOT NULL DEFAULT '',
    proposed_by     TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TEXT,
    created_at      TEXT NOT NULL
);
CREATE INDEX actions_conversation ON actions(conversation_id, created_at);
CREATE INDEX actions_task ON actions(task_id, created_at);
CREATE INDEX actions_run ON actions(run_ref);

-- +goose Down
DROP TABLE actions;
