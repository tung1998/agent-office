-- +goose Up
-- What a Burn looks for first: the project's roadmap, bugs, or its own pick.
ALTER TABLE burn_sessions ADD COLUMN work_order TEXT NOT NULL DEFAULT 'roadmap'; -- roadmap | bugs | auto

-- +goose Down
ALTER TABLE burn_sessions DROP COLUMN work_order;
