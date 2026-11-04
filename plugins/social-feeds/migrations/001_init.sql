-- +goose Up
-- The cached feed items, replaced per source on every successful refresh.
CREATE TABLE item (
    source       text NOT NULL,
    id           text NOT NULL,
    position     int NOT NULL,
    title        text NOT NULL DEFAULT '',
    excerpt      text NOT NULL DEFAULT '',
    url          text NOT NULL DEFAULT '',
    image        text NOT NULL DEFAULT '',
    type         text NOT NULL DEFAULT '',
    published_at timestamptz,
    PRIMARY KEY (source, id)
);

-- When each source was last refreshed and the last error (already redacted).
CREATE TABLE source_status (
    source       text PRIMARY KEY,
    refreshed_at timestamptz,
    error        text NOT NULL DEFAULT ''
);
