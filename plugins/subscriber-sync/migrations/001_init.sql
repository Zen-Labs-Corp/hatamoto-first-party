-- +goose Up
-- Pending deliveries. The email is kept only until the delivery succeeds, fails
-- for good, or is 24 hours old (the retry cron deletes it).
CREATE TABLE queue (
    id              bigserial PRIMARY KEY,
    submission_id   text NOT NULL UNIQUE,
    form_slug       text NOT NULL,
    email           text NOT NULL,
    first_name      text NOT NULL DEFAULT '',
    last_name       text NOT NULL DEFAULT '',
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL,
    lease_until     timestamptz NOT NULL,
    created_at      timestamptz NOT NULL
);
CREATE INDEX queue_next_attempt_at_idx ON queue (next_attempt_at);

-- What happened to each submission. No email, no name, no provider text.
CREATE TABLE log (
    id         bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL,
    form_slug  text NOT NULL DEFAULT '',
    status     text NOT NULL,
    reason     text NOT NULL DEFAULT '',
    code       integer
);
CREATE INDEX log_created_at_idx ON log (created_at);
