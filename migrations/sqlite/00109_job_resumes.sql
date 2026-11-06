-- How many times a job was put back to run after office restarted under it
-- (ADR-257): an automation's run goes on, at most a few times. And how many
-- times in a row a Burn piece was cut off by a restart (not a try of its own).

-- +goose Up
ALTER TABLE jobs ADD COLUMN resumes INTEGER NOT NULL DEFAULT 0;
ALTER TABLE burn_items ADD COLUMN restarts INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE burn_items DROP COLUMN restarts;
ALTER TABLE jobs DROP COLUMN resumes;
