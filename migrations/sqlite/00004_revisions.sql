-- +goose Up
-- Snapshot of an org model (as a portable Template) taken before each change,
-- so edits to a template or a project's model can be rolled back.
CREATE TABLE org_revisions (
    id           TEXT PRIMARY KEY,
    org_model_id TEXT NOT NULL REFERENCES org_models(id) ON DELETE CASCADE,
    action       TEXT NOT NULL,
    actor        TEXT NOT NULL DEFAULT '',
    agent_count  INTEGER NOT NULL DEFAULT 0,
    snapshot     TEXT NOT NULL CHECK (json_valid(snapshot)),
    created_at   TEXT NOT NULL
);
CREATE INDEX org_revisions_model ON org_revisions(org_model_id, created_at);

-- +goose Down
DROP TABLE org_revisions;
