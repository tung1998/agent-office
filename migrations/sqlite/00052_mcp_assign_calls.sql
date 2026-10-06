-- +goose Up
-- MCP gateway phases 3-4 (ADR-093): which agents get a server, the tools
-- that write but run without asking, where an imported server came from,
-- and a log of every tool call through the gateway.
ALTER TABLE mcp_servers ADD COLUMN agents TEXT NOT NULL DEFAULT '[]';        -- JSON: agent ids; [] = every agent
ALTER TABLE mcp_servers ADD COLUMN trusted_tools TEXT NOT NULL DEFAULT '[]'; -- JSON: tool names run without approval
ALTER TABLE mcp_servers ADD COLUMN origin_ref TEXT NOT NULL DEFAULT '';      -- JSON: the config it was moved from

CREATE TABLE mcp_calls (
    id              TEXT PRIMARY KEY,
    server_id       TEXT NOT NULL,
    server_name     TEXT NOT NULL,
    tool            TEXT NOT NULL,
    caller          TEXT NOT NULL DEFAULT '',   -- agent name, "" = a person's own CLI
    caller_kind     TEXT NOT NULL DEFAULT '',   -- claude | codex | api | person
    project_id      TEXT NOT NULL DEFAULT '',
    conversation_id TEXT NOT NULL DEFAULT '',
    job_id          TEXT NOT NULL DEFAULT '',
    action_id       TEXT NOT NULL DEFAULT '',   -- the proposal of a call waiting for approval
    status          TEXT NOT NULL,              -- ok | error | proposed | denied
    error           TEXT NOT NULL DEFAULT '',
    duration_ms     INTEGER NOT NULL DEFAULT 0,
    created_at      TEXT NOT NULL
);
CREATE INDEX mcp_calls_server ON mcp_calls (server_id, created_at);
CREATE INDEX mcp_calls_created ON mcp_calls (created_at);

-- +goose Down
DROP TABLE mcp_calls;
ALTER TABLE mcp_servers DROP COLUMN origin_ref;
ALTER TABLE mcp_servers DROP COLUMN trusted_tools;
ALTER TABLE mcp_servers DROP COLUMN agents;
