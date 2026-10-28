-- +goose Up
-- One row per (install, UTC day): the ping the install sent that day. Only the
-- three fields core sends are stored; the CHECKs repeat the guest's validation.
CREATE TABLE installs_daily (
    install text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    day     date NOT NULL,
    version text NOT NULL CHECK (length(version) <= 64),
    channel text NOT NULL CHECK (channel IN ('stable', 'edge', 'canary')),
    PRIMARY KEY (install, day)
);
CREATE INDEX installs_daily_day ON installs_daily (day, install);

-- Daily rows older than 13 months are folded into one row per (install, month)
-- holding the version and channel last seen that month. Install IDs are kept.
CREATE TABLE installs_monthly (
    install text NOT NULL CHECK (install ~ '^[0-9a-f]{32}$'),
    month   date NOT NULL,
    version text NOT NULL CHECK (length(version) <= 64),
    channel text NOT NULL CHECK (channel IN ('stable', 'edge', 'canary')),
    PRIMARY KEY (install, month)
);
CREATE INDEX installs_monthly_month ON installs_monthly (month);
