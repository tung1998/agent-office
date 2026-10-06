-- +goose Up
-- Change log (ADR-043): who (person/agent/automation), who approved, where
-- from (chat/job/task/proposal), which channel, and before/after.
ALTER TABLE audit_log ADD COLUMN actor_kind TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN actor_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN actor_name TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN approved_by TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN via TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN project_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN conversation_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN task_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN action_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN resource TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN resource_id TEXT NOT NULL DEFAULT '';
ALTER TABLE audit_log ADD COLUMN before_json TEXT CHECK (before_json IS NULL OR json_valid(before_json));
ALTER TABLE audit_log ADD COLUMN after_json TEXT CHECK (after_json IS NULL OR json_valid(after_json));
ALTER TABLE audit_log ADD COLUMN ok INTEGER NOT NULL DEFAULT 1;
-- older rows: actor "human:<email>" or a plain name; resource from "resource.verb"
UPDATE audit_log SET actor_kind = 'human', actor_name = substr(actor, 7), via = 'ui' WHERE actor LIKE 'human:%';
UPDATE audit_log SET actor_kind = 'system', actor_name = actor WHERE actor_kind = '';
UPDATE audit_log SET resource = CASE WHEN instr(action, '.') > 0 THEN substr(action, 1, instr(action, '.') - 1) ELSE action END, resource_id = target;
UPDATE audit_log SET ok = 0 WHERE action LIKE '%failed%' OR action LIKE '%throttled%';
CREATE INDEX audit_log_project ON audit_log(project_id, at);
CREATE INDEX audit_log_resource ON audit_log(resource, resource_id, at);
CREATE INDEX audit_log_actor ON audit_log(actor_kind, actor_name, at);
CREATE INDEX audit_log_conversation ON audit_log(conversation_id);
CREATE INDEX audit_log_job ON audit_log(job_id);
-- the job (chat answer/task run) an agent proposal came from
ALTER TABLE actions ADD COLUMN job_id TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE actions DROP COLUMN job_id;
DROP INDEX audit_log_job;
DROP INDEX audit_log_conversation;
DROP INDEX audit_log_actor;
DROP INDEX audit_log_resource;
DROP INDEX audit_log_project;
ALTER TABLE audit_log DROP COLUMN ok;
ALTER TABLE audit_log DROP COLUMN after_json;
ALTER TABLE audit_log DROP COLUMN before_json;
ALTER TABLE audit_log DROP COLUMN resource_id;
ALTER TABLE audit_log DROP COLUMN resource;
ALTER TABLE audit_log DROP COLUMN action_id;
ALTER TABLE audit_log DROP COLUMN task_id;
ALTER TABLE audit_log DROP COLUMN job_id;
ALTER TABLE audit_log DROP COLUMN conversation_id;
ALTER TABLE audit_log DROP COLUMN project_id;
ALTER TABLE audit_log DROP COLUMN via;
ALTER TABLE audit_log DROP COLUMN approved_by;
ALTER TABLE audit_log DROP COLUMN actor_name;
ALTER TABLE audit_log DROP COLUMN actor_id;
ALTER TABLE audit_log DROP COLUMN actor_kind;
