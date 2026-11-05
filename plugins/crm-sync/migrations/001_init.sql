-- +goose Up
-- Pending deliveries. The email and the submitted fields are kept only until
-- the delivery succeeds, fails for good, or is 24 hours old (the retry cron
-- deletes it).
CREATE TABLE queue (
    id              bigserial PRIMARY KEY,
    submission_id   text NOT NULL UNIQUE,
    form_slug       text NOT NULL,
    provider        text NOT NULL,
    email           text NOT NULL,
    fields          jsonb NOT NULL DEFAULT '{}'::jsonb,
    source_path     text NOT NULL DEFAULT '',
    submitted_at    timestamptz NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    lease_until     timestamptz NOT NULL,
    created_at      timestamptz NOT NULL
);
CREATE INDEX queue_next_attempt_at_idx ON queue (next_attempt_at);

-- What happened to each submission. No email, no field values, no provider text.
CREATE TABLE log (
    id            bigserial PRIMARY KEY,
    created_at    timestamptz NOT NULL,
    submission_id text NOT NULL DEFAULT '',
    form_slug     text NOT NULL DEFAULT '',
    provider      text NOT NULL DEFAULT '',
    status        text NOT NULL,
    record_id     text NOT NULL DEFAULT '',
    record_url    text NOT NULL DEFAULT '',
    code          integer,
    message       text NOT NULL DEFAULT ''
);
CREATE INDEX log_created_at_idx ON log (created_at);
