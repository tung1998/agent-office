-- +goose Up
-- The office's own MCP servers, reached by agent runs through its gateway
-- /mcp/s/<name> (spec 2026-10-01-mcp-gateway-design, ADR-091).
CREATE TABLE mcp_servers (
    id                TEXT PRIMARY KEY,
    name              TEXT NOT NULL UNIQUE,              -- a-z0-9 and "-": tools show as mcp__<name>__*
    kind              TEXT NOT NULL DEFAULT 'http',      -- http | stdio
    url               TEXT NOT NULL DEFAULT '',          -- http
    command           TEXT NOT NULL DEFAULT '',          -- stdio
    args              TEXT NOT NULL DEFAULT '[]',        -- stdio: JSON array
    env_enc           TEXT NOT NULL DEFAULT '',          -- stdio: JSON object, encrypted
    headers_enc       TEXT NOT NULL DEFAULT '',          -- http: JSON object, encrypted
    scope             TEXT NOT NULL DEFAULT 'machine',   -- machine | project:<id>
    origin            TEXT NOT NULL DEFAULT 'manual',    -- manual | claude | codex | mcp.json
    enabled           INTEGER NOT NULL DEFAULT 1,
    last_check_at     TEXT,
    last_check_status TEXT NOT NULL DEFAULT '',          -- "" (never) | ok | error
    last_check_error  TEXT NOT NULL DEFAULT '',
    last_tools        TEXT NOT NULL DEFAULT '[]',        -- JSON: [{name, description, read_only}]
    created_at        TEXT NOT NULL,
    updated_at        TEXT NOT NULL
);

-- +goose Down
DROP TABLE mcp_servers;
