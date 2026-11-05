-- +goose Up
-- Only hashes and ad click IDs are kept, never a raw email, phone or name.
-- Queue rows live until the conversion is sent, a platform answers with a
-- permanent error, or 24 hours have passed.
CREATE TABLE queue (
    id              bigserial PRIMARY KEY,
    submission_id   text NOT NULL,
    form_slug       text NOT NULL DEFAULT '',
    platform        text NOT NULL CHECK (platform IN ('google', 'tiktok', 'linkedin')),
    em_hash         text,
    em_google_hash  text,
    ph_hash         text,
    gclid           text,
    gbraid          text,
    wbraid          text,
    ttclid          text,
    li_fat_id       text,
    page_path       text NOT NULL DEFAULT '',
    consent         boolean NOT NULL DEFAULT false,
    submitted_at    timestamptz NOT NULL,
    attempts        integer NOT NULL DEFAULT 0,
    next_attempt_at timestamptz NOT NULL DEFAULT now(),
    lease_until     timestamptz,
    ids_joined      boolean NOT NULL DEFAULT false,
    created_at      timestamptz NOT NULL DEFAULT now(),
    UNIQUE (submission_id, platform)
);
CREATE INDEX queue_next_attempt_at ON queue (next_attempt_at);

-- Click IDs posted by the page script, joined to the queue at the first send.
-- Kept for one hour.
CREATE TABLE clicks (
    submission_id text PRIMARY KEY,
    ids           jsonb NOT NULL DEFAULT '{}',
    created_at    timestamptz NOT NULL DEFAULT now()
);

-- The delivery log: outcomes only, never a value, a hash, a click ID or a
-- token. Kept for 30 days.
CREATE TABLE log (
    id            bigserial PRIMARY KEY,
    created_at    timestamptz NOT NULL DEFAULT now(),
    submission_id text NOT NULL DEFAULT '',
    form_slug     text NOT NULL DEFAULT '',
    platform      text NOT NULL DEFAULT '',
    status        text NOT NULL CHECK (status IN ('sent', 'failed', 'skipped', 'retrying', 'test')),
    reason        text NOT NULL DEFAULT '',
    code          text NOT NULL DEFAULT '',
    message       text NOT NULL DEFAULT ''
);
CREATE INDEX log_created_at ON log (created_at DESC);
