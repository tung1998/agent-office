-- A task: a goal handed to the whole org model of a project, run according to
-- its governance (solo / hierarchy / council). Every agent step is recorded.

-- +goose NO TRANSACTION
-- +goose Up
CREATE TABLE tasks (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    title        TEXT NOT NULL DEFAULT '',
    goal         TEXT NOT NULL,
    mode         TEXT NOT NULL,              -- single | hierarchy | council
    status       TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed', 'cancelled', 'rejected')),
    result       TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    budget_usd   REAL NOT NULL DEFAULT 0,    -- 0 = no task cap (daily limits still apply)
    cost_usd     REAL NOT NULL DEFAULT 0,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT
);
CREATE INDEX tasks_project ON tasks(project_id, created_at);

CREATE TABLE task_steps (
    id          TEXT PRIMARY KEY,
    task_id     TEXT NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    seq         INTEGER NOT NULL,
    phase       TEXT NOT NULL,               -- plan | vote | revise | work | review | synthesize
    agent_id    TEXT REFERENCES agents(id) ON DELETE SET NULL,
    agent_key   TEXT NOT NULL DEFAULT '',
    agent_name  TEXT NOT NULL DEFAULT '',
    instruction TEXT NOT NULL DEFAULT '',
    output      TEXT NOT NULL DEFAULT '',
    data        TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(data)), -- parsed plan / vote / verdict
    tools       TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(tools)),
    status      TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed', 'skipped')),
    error       TEXT NOT NULL DEFAULT '',
    cost_usd    REAL,
    run_id      TEXT REFERENCES runs(id) ON DELETE SET NULL,
    started_at  TEXT NOT NULL,
    finished_at TEXT
);
CREATE INDEX task_steps_task ON task_steps(task_id, seq);

-- Patches now come from chat messages or task steps.
PRAGMA foreign_keys = OFF;
CREATE TABLE patches_new (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      TEXT REFERENCES messages(id) ON DELETE CASCADE,
    task_id         TEXT REFERENCES tasks(id) ON DELETE CASCADE,
    step_id         TEXT REFERENCES task_steps(id) ON DELETE CASCADE,
    diff            TEXT NOT NULL,
    files           TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(files)),
    status          TEXT NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'applied', 'rejected', 'failed')),
    detail          TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TEXT,
    created_at      TEXT NOT NULL,
    CHECK (conversation_id IS NOT NULL OR task_id IS NOT NULL)
);
INSERT INTO patches_new (id, conversation_id, message_id, diff, files, status, detail, decided_by, decided_at, created_at)
    SELECT id, conversation_id, message_id, diff, files, status, detail, decided_by, decided_at, created_at FROM patches;
DROP TABLE patches;
ALTER TABLE patches_new RENAME TO patches;
CREATE INDEX patches_conversation ON patches(conversation_id);
CREATE INDEX patches_task ON patches(task_id);
PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
DELETE FROM patches WHERE conversation_id IS NULL;
CREATE TABLE patches_old (
    id              TEXT PRIMARY KEY,
    conversation_id TEXT NOT NULL REFERENCES conversations(id) ON DELETE CASCADE,
    message_id      TEXT NOT NULL REFERENCES messages(id) ON DELETE CASCADE,
    diff            TEXT NOT NULL,
    files           TEXT NOT NULL DEFAULT '[]',
    status          TEXT NOT NULL DEFAULT 'pending',
    detail          TEXT NOT NULL DEFAULT '',
    decided_by      TEXT NOT NULL DEFAULT '',
    decided_at      TEXT,
    created_at      TEXT NOT NULL
);
INSERT INTO patches_old SELECT id, conversation_id, message_id, diff, files, status, detail, decided_by, decided_at, created_at FROM patches;
DROP TABLE patches;
ALTER TABLE patches_old RENAME TO patches;
PRAGMA foreign_keys = ON;
DROP TABLE task_steps;
DROP TABLE tasks;
