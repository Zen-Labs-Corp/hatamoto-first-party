-- +goose Up
CREATE TABLE feeds (
    key           text        PRIMARY KEY,
    url           text        NOT NULL,
    name          text        NOT NULL,
    grp           integer     NOT NULL DEFAULT 0,
    last_attempt  timestamptz,
    status        text        NOT NULL DEFAULT '',
    etag          text        NOT NULL DEFAULT '',
    last_modified text        NOT NULL DEFAULT '',
    item_count    integer     NOT NULL DEFAULT 0,
    site_url      text        NOT NULL DEFAULT ''
);

CREATE TABLE items (
    feed_key     text        NOT NULL,
    guid         text        NOT NULL,
    title        text        NOT NULL,
    link         text        NOT NULL,
    excerpt      text        NOT NULL,
    published_at timestamptz,
    fetched_at   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (feed_key, guid)
);
