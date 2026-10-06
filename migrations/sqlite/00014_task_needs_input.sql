-- A task may stop to ask the person something ("needs_input"); answering
-- starts it again. SQLite cannot change a CHECK constraint, so the table is
-- rebuilt with foreign keys off (steps, patches and actions reference it).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE tasks_new (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    title        TEXT NOT NULL DEFAULT '',
    goal         TEXT NOT NULL,
    mode         TEXT NOT NULL,              -- single | hierarchy | council
    status       TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed', 'cancelled', 'rejected', 'needs_input')),
    result       TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    budget_usd   REAL NOT NULL DEFAULT 0,    -- 0 = no task cap (daily limits still apply)
    cost_usd     REAL NOT NULL DEFAULT 0,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT,
    attachments  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attachments)),
    mode_level   TEXT NOT NULL DEFAULT 'propose'
);
INSERT INTO tasks_new (id, project_id, title, goal, mode, status, result, detail, budget_usd, cost_usd, created_by, created_at, finished_at, attachments, mode_level)
    SELECT id, project_id, title, goal, mode, status, result, detail, budget_usd, cost_usd, created_by, created_at, finished_at, attachments, mode_level FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_new RENAME TO tasks;
CREATE INDEX tasks_project ON tasks(project_id, created_at);

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

CREATE TABLE tasks_old (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    title        TEXT NOT NULL DEFAULT '',
    goal         TEXT NOT NULL,
    mode         TEXT NOT NULL,
    status       TEXT NOT NULL CHECK (status IN ('running', 'done', 'failed', 'cancelled', 'rejected')),
    result       TEXT NOT NULL DEFAULT '',
    detail       TEXT NOT NULL DEFAULT '',
    budget_usd   REAL NOT NULL DEFAULT 0,
    cost_usd     REAL NOT NULL DEFAULT 0,
    created_by   TEXT NOT NULL DEFAULT '',
    created_at   TEXT NOT NULL,
    finished_at  TEXT,
    attachments  TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(attachments)),
    mode_level   TEXT NOT NULL DEFAULT 'propose'
);
INSERT INTO tasks_old SELECT id, project_id, title, goal, mode,
    CASE status WHEN 'needs_input' THEN 'failed' ELSE status END,
    result, detail, budget_usd, cost_usd, created_by, created_at, finished_at, attachments, mode_level FROM tasks;
DROP TABLE tasks;
ALTER TABLE tasks_old RENAME TO tasks;
CREATE INDEX tasks_project ON tasks(project_id, created_at);

PRAGMA foreign_keys = ON;
