-- +goose Up
-- Older audit rows, read the way audit.Entry reads actors now (ADR-043
-- review): "user:<id>" is a person, "auto:<name>" an automation; the project
-- comes from the detail or, for a project, from its id.
UPDATE audit_log SET actor_kind = 'human', actor_id = substr(actor, 6), actor_name = substr(actor, 6), via = 'ui'
WHERE actor LIKE 'user:%' AND actor_kind = 'system';
UPDATE audit_log SET actor_kind = 'automation', actor_name = substr(actor, 6), via = 'automation'
WHERE actor LIKE 'auto:%' AND actor_kind = 'system';
UPDATE audit_log SET project_id = json_extract(detail, '$.project')
WHERE project_id = '' AND json_type(detail, '$.project') = 'text';
UPDATE audit_log SET project_id = target WHERE project_id = '' AND resource = 'project' AND target LIKE 'rep_%';

-- +goose Down
SELECT 1;
