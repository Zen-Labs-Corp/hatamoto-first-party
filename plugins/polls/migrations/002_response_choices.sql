-- +goose Up

-- The options a response chose, as [{"field": ..., "option": ...}], so the
-- tallies can be rebuilt from the response rows. recounted marks the rows the
-- last Recount confirmed.
ALTER TABLE poll_response ADD COLUMN choices jsonb;
ALTER TABLE poll_response ADD COLUMN recounted timestamptz;

-- One Recount at a time per form: the holder's run id and when the lease runs
-- out, so a Recount that died does not block the form for ever.
CREATE TABLE poll_recount_lock (
    form_slug  text PRIMARY KEY,
    run_id     text NOT NULL,
    expires_at timestamptz NOT NULL
);
