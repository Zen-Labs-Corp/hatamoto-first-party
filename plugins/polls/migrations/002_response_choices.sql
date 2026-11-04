-- +goose Up

-- The options a response chose, as [{"field": ..., "option": ...}], so the
-- tallies can be rebuilt from the response rows. recounted marks the rows the
-- last Recount confirmed.
ALTER TABLE poll_response ADD COLUMN choices jsonb;
ALTER TABLE poll_response ADD COLUMN recounted timestamptz;
