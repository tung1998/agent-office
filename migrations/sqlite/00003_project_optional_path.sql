-- A project may have no folder: it is then a machine-wide helper.
-- SQLite cannot drop a column constraint, so the table is rebuilt. Foreign
-- keys are switched off meanwhile, otherwise dropping the old table would
-- cascade-delete every project's org model.

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE repos_new (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL DEFAULT '',   -- '' = machine-wide helper
    git_remote  TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
INSERT INTO repos_new SELECT id, name, path, git_remote, description, created_at, updated_at FROM repos;
DROP TABLE repos;
ALTER TABLE repos_new RENAME TO repos;
CREATE UNIQUE INDEX repos_path ON repos(path) WHERE path <> '';

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;

CREATE TABLE repos_old (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    git_remote  TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);
INSERT INTO repos_old SELECT id, name, CASE WHEN path = '' THEN 'helper:' || id ELSE path END, git_remote, description, created_at, updated_at FROM repos;
DROP TABLE repos;
ALTER TABLE repos_old RENAME TO repos;

PRAGMA foreign_keys = ON;
