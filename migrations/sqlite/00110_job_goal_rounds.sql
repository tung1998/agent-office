-- How many rounds of its goal a run has started (ADR-258): a run put back
-- after office restarted under it goes on from there, not from round 1.

-- +goose Up
ALTER TABLE jobs ADD COLUMN goal_rounds INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN goal_rounds;
