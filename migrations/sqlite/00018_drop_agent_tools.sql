-- +goose Up
-- permissions.tools was never enforced: what an agent may use comes from its
-- permission package, capabilities and commands (ADR-035).
UPDATE agents SET permissions = json_remove(permissions, '$.tools') WHERE json_extract(permissions, '$.tools') IS NOT NULL;

-- +goose Down
SELECT 1;
