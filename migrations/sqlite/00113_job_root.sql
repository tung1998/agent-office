-- The job a run descends from (ADR-263): a workflow step's or a hand-off's
-- job keeps the job that started it all (root_job_id) and, when that was an
-- automation's run, the automation (root_automation_id), so its daily cost
-- cap and its reports count the whole tree.

-- +goose Up
ALTER TABLE jobs ADD COLUMN root_job_id TEXT NOT NULL DEFAULT '';
ALTER TABLE jobs ADD COLUMN root_automation_id TEXT NOT NULL DEFAULT '';
CREATE INDEX jobs_root_automation ON jobs(root_automation_id, created_at) WHERE root_automation_id != '';

-- +goose Down
DROP INDEX jobs_root_automation;
ALTER TABLE jobs DROP COLUMN root_automation_id;
ALTER TABLE jobs DROP COLUMN root_job_id;
