-- +goose Up
-- AI connections: an API account or a local CLI that can run models.
CREATE TABLE providers (
    id              TEXT PRIMARY KEY,
    name            TEXT NOT NULL UNIQUE,
    kind            TEXT NOT NULL CHECK (kind IN ('anthropic', 'openai', 'openai_compatible', 'claude_cli', 'codex_cli')),
    base_url        TEXT NOT NULL DEFAULT '',
    api_key_enc     TEXT NOT NULL DEFAULT '',   -- AES-GCM, never returned by the API
    api_key_env     TEXT NOT NULL DEFAULT '',   -- alternative: read the key from this env var
    api_key_hint    TEXT NOT NULL DEFAULT '',   -- last 4 chars for display
    tier_models     TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(tier_models)), -- {"strong":..,"balanced":..,"fast":..}
    models          TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(models)),      -- last fetched model ids
    is_default      INTEGER NOT NULL DEFAULT 0,
    enabled         INTEGER NOT NULL DEFAULT 1,
    status          TEXT NOT NULL DEFAULT 'unknown' CHECK (status IN ('unknown', 'ok', 'error')),
    status_detail   TEXT NOT NULL DEFAULT '',
    checked_at      TEXT,
    created_at      TEXT NOT NULL,
    updated_at      TEXT NOT NULL
);

CREATE TABLE repos (
    id          TEXT PRIMARY KEY,
    name        TEXT NOT NULL,
    path        TEXT NOT NULL UNIQUE,
    git_remote  TEXT NOT NULL DEFAULT '',
    description TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    updated_at  TEXT NOT NULL
);

-- An org model: templates live in the library (repo_id IS NULL); applying one
-- to a repo copies it into an instance (repo_id set) that is edited separately.
CREATE TABLE org_models (
    id                 TEXT PRIMARY KEY,
    repo_id            TEXT REFERENCES repos(id) ON DELETE CASCADE,
    source_template_id TEXT REFERENCES org_models(id) ON DELETE SET NULL,
    key                TEXT NOT NULL,
    name               TEXT NOT NULL,
    description        TEXT NOT NULL DEFAULT '',
    kind               TEXT NOT NULL CHECK (kind IN ('solo', 'team', 'council', 'custom')),
    governance         TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(governance)),
    builtin            INTEGER NOT NULL DEFAULT 0,
    created_at         TEXT NOT NULL,
    updated_at         TEXT NOT NULL
);
CREATE UNIQUE INDEX org_models_template_key ON org_models(key) WHERE repo_id IS NULL;
CREATE UNIQUE INDEX org_models_repo ON org_models(repo_id) WHERE repo_id IS NOT NULL;

CREATE TABLE agents (
    id            TEXT PRIMARY KEY,
    org_model_id  TEXT NOT NULL REFERENCES org_models(id) ON DELETE CASCADE,
    key           TEXT NOT NULL,
    name          TEXT NOT NULL,
    tier          TEXT NOT NULL CHECK (tier IN ('lead', 'manager', 'worker')),
    role          TEXT NOT NULL DEFAULT '',
    description   TEXT NOT NULL DEFAULT '',
    reports_to    TEXT NOT NULL DEFAULT '[]' CHECK (json_valid(reports_to)),
    provider_id   TEXT REFERENCES providers(id) ON DELETE SET NULL,
    model_tier    TEXT NOT NULL DEFAULT 'balanced' CHECK (model_tier IN ('strong', 'balanced', 'fast')),
    llm_model     TEXT NOT NULL DEFAULT '',   -- overrides the provider's tier model when set
    instructions  TEXT NOT NULL DEFAULT '',
    permissions   TEXT NOT NULL DEFAULT '{}' CHECK (json_valid(permissions)),
    sort          INTEGER NOT NULL DEFAULT 0,
    created_at    TEXT NOT NULL,
    updated_at    TEXT NOT NULL,
    UNIQUE (org_model_id, key)
);

-- +goose Down
DROP TABLE agents;
DROP TABLE org_models;
DROP TABLE repos;
DROP TABLE providers;
