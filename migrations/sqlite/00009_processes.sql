-- +goose Up
-- Commands office runs for a project, like pm2: dev servers, builds, tests.
-- Only admins define them; the command runs through `sh -c` in cwd.
CREATE TABLE processes (
    id           TEXT PRIMARY KEY,
    project_id   TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    name         TEXT NOT NULL,
    command      TEXT NOT NULL,
    cwd          TEXT NOT NULL DEFAULT '',      -- relative to the project folder
    kind         TEXT NOT NULL DEFAULT 'service' CHECK (kind IN ('service', 'job')), -- long-running or runs to completion
    source       TEXT NOT NULL DEFAULT '',      -- where it was detected (package.json, Makefile…)
    autostart    INTEGER NOT NULL DEFAULT 0,    -- start when office starts
    autorestart  INTEGER NOT NULL DEFAULT 0,    -- restart after a crash (services)
    created_at   TEXT NOT NULL,
    updated_at   TEXT NOT NULL,
    UNIQUE (project_id, name)
);

-- +goose Down
DROP TABLE processes;
