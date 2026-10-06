-- +goose Up
-- The person's own MCP servers are tools, available at every level: agents
-- that picked their capabilities one by one get "tools.mcp" too.
UPDATE agents
SET permissions = json_insert(permissions, '$.caps[#]', 'tools.mcp')
WHERE json_type(permissions, '$.caps') = 'array'
  AND NOT EXISTS (SELECT 1 FROM json_each(agents.permissions, '$.caps') WHERE value = 'tools.mcp');

-- +goose Down
SELECT 1;
