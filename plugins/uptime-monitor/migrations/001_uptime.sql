-- +goose Up
-- Monitored addresses with their current state (at most 20, enforced by the
-- plugin). One row at most is the site itself.
CREATE TABLE monitors (
    id          bigserial PRIMARY KEY,
    name        text NOT NULL,
    url         text NOT NULL,
    interval_s  integer NOT NULL DEFAULT 300,
    keyword     text NOT NULL DEFAULT '',
    max_ms      integer NOT NULL DEFAULT 0,
    is_site     boolean NOT NULL DEFAULT false,
    created_at  timestamptz NOT NULL DEFAULT now(),
    status      text NOT NULL DEFAULT 'pending'
                CHECK (status IN ('pending', 'up', 'slow', 'down', 'config')),
    last_check  timestamptz,
    next_check  timestamptz NOT NULL DEFAULT now(),
    last_ms     integer,
    last_status integer,
    last_error  text NOT NULL DEFAULT '',
    fail_count  integer NOT NULL DEFAULT 0,
    down_since  timestamptz,
    alerted_down boolean NOT NULL DEFAULT false
);
CREATE UNIQUE INDEX monitors_one_site ON monitors (is_site) WHERE is_site;
CREATE INDEX monitors_next_check ON monitors (next_check);

-- One row per check, kept 30 days. ok is NULL for a configuration problem so
-- it counts neither as up nor as down.
CREATE TABLE checks (
    monitor_id  bigint NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    at          timestamptz NOT NULL,
    http_status integer,
    ms          integer,
    ok          boolean,
    cause       text NOT NULL DEFAULT ''
);
CREATE INDEX checks_monitor_at ON checks (monitor_id, at);

-- Outages, kept 13 months.
CREATE TABLE incidents (
    id           bigserial PRIMARY KEY,
    monitor_id   bigint NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    started_at   timestamptz NOT NULL,
    ended_at     timestamptz,
    cause        text NOT NULL DEFAULT '',
    alerted_down boolean NOT NULL DEFAULT false,
    alerted_up   boolean NOT NULL DEFAULT false
);
CREATE INDEX incidents_monitor_started ON incidents (monitor_id, started_at);

-- Per UTC day rollup, kept 13 months.
CREATE TABLE daily (
    monitor_id bigint NOT NULL REFERENCES monitors (id) ON DELETE CASCADE,
    day        date NOT NULL,
    checks     integer NOT NULL,
    up_checks  integer NOT NULL,
    uptime_pct numeric(6, 3) NOT NULL,
    avg_ms     integer,
    UNIQUE (monitor_id, day)
);

-- Certificate expiry per host as read from public certificate logs.
-- warned lists the day thresholds already mailed for this not_after.
CREATE TABLE certs (
    host         text NOT NULL UNIQUE,
    not_after    timestamptz,
    issuer       text NOT NULL DEFAULT '',
    looked_up_at timestamptz,
    lookup_error text NOT NULL DEFAULT '',
    warned       integer[] NOT NULL DEFAULT '{}'
);

-- Alert mail waiting for the per-minute send limit.
CREATE TABLE outbox (
    id         bigserial PRIMARY KEY,
    created_at timestamptz NOT NULL DEFAULT now(),
    subject    text NOT NULL,
    body       text NOT NULL,
    recipients text[] NOT NULL,
    attempts   integer NOT NULL DEFAULT 0,
    -- set while a run is sending the mail, so two runs never send it twice
    claimed_at timestamptz
);
