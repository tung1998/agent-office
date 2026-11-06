-- A turn that died on a passing API failure (error code api_transient) goes
-- on by itself a while later (ADR-260): when (retry_at, NULL = not planned)
-- and which try of how many in a row it is (retry_n).

-- +goose Up
ALTER TABLE jobs ADD COLUMN retry_at TEXT;
ALTER TABLE jobs ADD COLUMN retry_n INTEGER NOT NULL DEFAULT 0;

-- +goose Down
ALTER TABLE jobs DROP COLUMN retry_n;
ALTER TABLE jobs DROP COLUMN retry_at;
