-- +goose Up
-- MCP resources (ADR-091 follow-up): what resources/list found at a server's
-- last check, alongside its tools.
ALTER TABLE mcp_servers ADD COLUMN last_resources TEXT NOT NULL DEFAULT '[]'; -- JSON []storage.MCPResource

-- +goose Down
ALTER TABLE mcp_servers DROP COLUMN last_resources;
