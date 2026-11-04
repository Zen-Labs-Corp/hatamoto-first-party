-- +goose Up
-- One row per handled form submission: the outcome of the send, never a field
-- value, a hash or the token.
CREATE TABLE deliveries (
    id            bigserial PRIMARY KEY,
    submission_id text NOT NULL UNIQUE,
    form_slug     text NOT NULL DEFAULT '',
    status        text NOT NULL CHECK (status IN ('sent', 'failed', 'skipped')),
    reason        text NOT NULL DEFAULT '',
    meta_code     integer,
    meta_message  text,
    test          boolean NOT NULL DEFAULT false,
    created_at    timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX deliveries_created_at ON deliveries (created_at DESC);
