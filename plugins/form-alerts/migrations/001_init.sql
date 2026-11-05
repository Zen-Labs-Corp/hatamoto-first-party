-- +goose Up
-- Pending messages, one row per submission and channel. The row holds the
-- rendered message (which carries the chosen field values) only until it is
-- delivered, rejected for good, or six hours old (the crons delete it). No
-- webhook URL or token is stored here.
CREATE TABLE queue (
    id            bigserial PRIMARY KEY,
    submission_id text NOT NULL,
    form_slug     text NOT NULL,
    channel       text NOT NULL,
    body          text NOT NULL,
    attempts      integer NOT NULL DEFAULT 0,
    next_at       timestamptz NOT NULL,
    lease_until   timestamptz NOT NULL,
    created_at    timestamptz NOT NULL,
    UNIQUE (submission_id, channel)
);
CREATE INDEX queue_next_at_idx ON queue (next_at);

-- What happened to each message. No field values, no URL, no token.
CREATE TABLE log (
    id            bigserial PRIMARY KEY,
    created_at    timestamptz NOT NULL,
    submission_id text NOT NULL,
    form_slug     text NOT NULL,
    channel       text NOT NULL,
    status        text NOT NULL,
    code          integer NOT NULL DEFAULT 0
);
CREATE INDEX log_created_at_idx ON log (created_at);

-- +goose Down
DROP TABLE log;
DROP TABLE queue;
