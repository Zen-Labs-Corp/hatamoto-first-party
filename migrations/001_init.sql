-- +goose Up
-- The Google connection. Tokens are AES-GCM encrypted (see crypto.go).
CREATE TABLE connection (
    id                integer PRIMARY KEY CHECK (id = 1),
    refresh_token     text NOT NULL,
    access_token      text NOT NULL DEFAULT '',
    access_expires_at timestamptz,
    scope             text NOT NULL DEFAULT '',
    site_url          text NOT NULL DEFAULT '',
    connected_at      timestamptz NOT NULL DEFAULT now()
);

-- One row per (day, query, page) of the Search Analytics export.
CREATE TABLE perf_daily (
    day          date NOT NULL,
    query        text NOT NULL,
    page         text NOT NULL,
    clicks       integer NOT NULL,
    impressions  integer NOT NULL,
    ctr          double precision NOT NULL,
    avg_position double precision NOT NULL,
    PRIMARY KEY (day, query, page)
);
CREATE INDEX perf_daily_page ON perf_daily (page);

-- A day is imported once its rows are stored (even when there were none).
CREATE TABLE imported_days (
    day         date PRIMARY KEY,
    site_url    text NOT NULL,
    row_count   integer NOT NULL,
    imported_at timestamptz NOT NULL DEFAULT now()
);

-- Where the import of a large day stopped (rows are fetched page by page, a
-- run may end before the day does; the next run resumes at next_row).
CREATE TABLE import_progress (
    day        date PRIMARY KEY,
    site_url   text NOT NULL,
    next_row   integer NOT NULL,
    stored     integer NOT NULL,
    updated_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE import_runs (
    id         bigserial PRIMARY KEY,
    ran_at     timestamptz NOT NULL DEFAULT now(),
    days       integer NOT NULL,
    row_count  integer NOT NULL,
    error      text NOT NULL DEFAULT ''
);

CREATE TABLE sitemap_submissions (
    id           bigserial PRIMARY KEY,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    site_url     text NOT NULL,
    sitemap_url  text NOT NULL,
    error        text NOT NULL DEFAULT ''
);

CREATE TABLE inspections (
    id           bigserial PRIMARY KEY,
    inspected_at timestamptz NOT NULL DEFAULT now(),
    site_url     text NOT NULL,
    url          text NOT NULL,
    verdict      text NOT NULL DEFAULT '',
    coverage     text NOT NULL DEFAULT '',
    result       jsonb NOT NULL DEFAULT '{}'
);
CREATE INDEX inspections_url ON inspections (url, inspected_at DESC);
