-- +goose Up
-- Plugin secrets generated once (the signing key of the form's time token).
CREATE TABLE meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- Event submissions from the site's form. Holds the submitter's email: it is
-- never copied into an event.
CREATE TABLE submissions (
    id          bigserial PRIMARY KEY,
    status      text NOT NULL DEFAULT 'pending' CHECK (status IN ('pending', 'approved', 'published', 'rejected')),
    title       text NOT NULL,
    start_at    text NOT NULL,
    end_at      text NOT NULL DEFAULT '',
    venue       text NOT NULL DEFAULT '',
    description text NOT NULL DEFAULT '',
    email       text NOT NULL,
    created_at  timestamptz NOT NULL DEFAULT now(),
    updated_at  timestamptz NOT NULL DEFAULT now(),
    notified    boolean NOT NULL DEFAULT false
);

-- The review list and the pending count read by status.
CREATE INDEX submissions_status_idx ON submissions (status, created_at);

-- The same person sending the same event twice while it waits is one submission.
CREATE UNIQUE INDEX submissions_dup_idx ON submissions (lower(email), lower(title), start_at)
    WHERE status IN ('pending', 'approved');
