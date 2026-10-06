-- +goose Up
-- ADR-074 security fix: compute effective permissions once per job, not per
-- trigger source. Prevents webhook/PR/bot messages from running with full access.
-- job.full_access_by = the admin who enabled it (validated at compute time);
-- "" means permission was not full access or did not pass validation.
ALTER TABLE jobs ADD COLUMN full_access BOOLEAN NOT NULL DEFAULT 0;
ALTER TABLE jobs ADD COLUMN full_access_by TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE jobs DROP COLUMN full_access;
ALTER TABLE jobs DROP COLUMN full_access_by;
