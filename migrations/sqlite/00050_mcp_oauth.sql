-- +goose Up
-- OAuth of an MCP server behind the gateway (ADR-092): what office found
-- about its authorization server, its client and the tokens, as one
-- encrypted JSON object. Kept apart from the form's fields so a login never
-- races an edit.
ALTER TABLE mcp_servers ADD COLUMN oauth_enc TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE mcp_servers DROP COLUMN oauth_enc;
