-- +goose Up
-- Daily site-wide totals (GetRankAndTrafficStats).
CREATE TABLE traffic_daily (
    day         date PRIMARY KEY,
    clicks      integer NOT NULL DEFAULT 0,
    impressions integer NOT NULL DEFAULT 0
);

-- Bing's weekly query rows, one per (week, query).
CREATE TABLE query_stats (
    week                    date NOT NULL,
    query                   text NOT NULL,
    clicks                  integer NOT NULL DEFAULT 0,
    impressions             integer NOT NULL DEFAULT 0,
    avg_click_position      double precision NOT NULL DEFAULT 0,
    avg_impression_position double precision NOT NULL DEFAULT 0,
    PRIMARY KEY (week, query)
);
CREATE INDEX query_stats_week ON query_stats (week);

-- Bing's weekly page rows, one per (week, page).
CREATE TABLE page_stats (
    week                    date NOT NULL,
    page                    text NOT NULL,
    clicks                  integer NOT NULL DEFAULT 0,
    impressions             integer NOT NULL DEFAULT 0,
    avg_click_position      double precision NOT NULL DEFAULT 0,
    avg_impression_position double precision NOT NULL DEFAULT 0,
    PRIMARY KEY (week, page)
);
CREATE INDEX page_stats_week ON page_stats (week);

-- Latest crawl issues snapshot; issues is Bing's flags value.
CREATE TABLE crawl_issues (
    url       text PRIMARY KEY,
    http_code integer NOT NULL DEFAULT 0,
    issues    integer NOT NULL DEFAULT 0,
    in_links  integer NOT NULL DEFAULT 0,
    seen_at   timestamptz NOT NULL
);

-- Latest sitemap (feed) snapshot.
CREATE TABLE feeds (
    url             text PRIMARY KEY,
    type            text NOT NULL DEFAULT '',
    status          text NOT NULL DEFAULT '',
    submitted_at    timestamptz,
    last_crawled_at timestamptz,
    url_count       integer NOT NULL DEFAULT 0,
    seen_at         timestamptz NOT NULL
);

CREATE TABLE sitemap_submissions (
    id           bigserial PRIMARY KEY,
    submitted_at timestamptz NOT NULL DEFAULT now(),
    feed_url     text NOT NULL,
    error        text NOT NULL DEFAULT ''
);

CREATE TABLE import_runs (
    id          bigserial PRIMARY KEY,
    started_at  timestamptz NOT NULL,
    finished_at timestamptz NOT NULL,
    ok          boolean NOT NULL,
    error       text NOT NULL DEFAULT ''
);
CREATE INDEX import_runs_started ON import_runs (started_at);
