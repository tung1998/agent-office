-- ADR-224: accounts see only their projects. The role 'manager' (a project
-- admin) manages the projects it created or was added to; 'admin' is the
-- office's admin and sees everything. SQLite cannot change a CHECK, so the
-- users table is rebuilt with foreign keys off (as 00003 does for repos).

-- +goose NO TRANSACTION
-- +goose Up
PRAGMA foreign_keys = OFF;

CREATE TABLE users_new (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name          TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL CHECK (role IN ('admin', 'manager', 'member')),
    password_hash TEXT NOT NULL,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    last_login_at TEXT,
    must_change   INTEGER NOT NULL DEFAULT 0,
    discord_id    TEXT NOT NULL DEFAULT '',
    telegram_id   TEXT NOT NULL DEFAULT ''
);
INSERT INTO users_new (id, email, name, role, password_hash, disabled, created_at, updated_at, last_login_at, must_change, discord_id, telegram_id)
SELECT id, email, name, role, password_hash, disabled, created_at, updated_at, last_login_at, must_change, discord_id, telegram_id FROM users;
DROP TABLE users;
ALTER TABLE users_new RENAME TO users;

-- the projects an account other than an admin sees
CREATE TABLE project_members (
    project_id TEXT NOT NULL REFERENCES repos(id) ON DELETE CASCADE,
    user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
    created_at TEXT NOT NULL,
    PRIMARY KEY (project_id, user_id)
);
CREATE INDEX project_members_user ON project_members(user_id);

-- members saw every project until now: they keep seeing those
INSERT INTO project_members (project_id, user_id, created_at)
SELECT r.id, u.id, strftime('%Y-%m-%dT%H:%M:%f', 'now') || '000000Z' FROM users u CROSS JOIN repos r WHERE u.role = 'member';

PRAGMA foreign_keys = ON;

-- +goose Down
PRAGMA foreign_keys = OFF;
DROP TABLE project_members;
CREATE TABLE users_old (
    id            TEXT PRIMARY KEY,
    email         TEXT NOT NULL UNIQUE COLLATE NOCASE,
    name          TEXT NOT NULL DEFAULT '',
    role          TEXT NOT NULL CHECK (role IN ('admin', 'member')),
    password_hash TEXT NOT NULL,
    disabled      INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    last_login_at TEXT,
    must_change   INTEGER NOT NULL DEFAULT 0,
    discord_id    TEXT NOT NULL DEFAULT '',
    telegram_id   TEXT NOT NULL DEFAULT ''
);
INSERT INTO users_old (id, email, name, role, password_hash, disabled, created_at, updated_at, last_login_at, must_change, discord_id, telegram_id)
SELECT id, email, name, CASE WHEN role = 'manager' THEN 'member' ELSE role END, password_hash, disabled, created_at, updated_at, last_login_at, must_change, discord_id, telegram_id FROM users;
DROP TABLE users;
ALTER TABLE users_old RENAME TO users;
PRAGMA foreign_keys = ON;
