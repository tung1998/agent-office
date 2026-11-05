-- Where a graph of steps is (ADR-256): its steps' results, what waits and
-- runs, the cards and sub-runs it waits on. The next office goes on from
-- there after a restart. '' = none (a coordinator's run, a run from before).

-- +goose Up
ALTER TABLE workflow_runs ADD COLUMN checkpoint TEXT NOT NULL DEFAULT '';

-- +goose Down
ALTER TABLE workflow_runs DROP COLUMN checkpoint;
