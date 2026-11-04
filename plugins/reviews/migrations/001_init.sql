-- +goose Up
CREATE TABLE reviews (
    source       text        NOT NULL,
    ext_id       text        NOT NULL,
    author       text        NOT NULL DEFAULT '',
    author_url   text        NOT NULL DEFAULT '',
    photo_url    text        NOT NULL DEFAULT '',
    rating       integer     NOT NULL CHECK (rating BETWEEN 1 AND 5),
    body         text        NOT NULL DEFAULT '',
    published_at timestamptz NOT NULL,
    link         text        NOT NULL DEFAULT '',
    PRIMARY KEY (source, ext_id)
);

CREATE INDEX reviews_source_published_idx ON reviews (source, published_at DESC);

CREATE TABLE sources (
    source       text PRIMARY KEY,
    rating       numeric,
    review_count integer NOT NULL DEFAULT 0,
    profile_url  text NOT NULL DEFAULT '',
    name         text NOT NULL DEFAULT '',
    attempted_at timestamptz,
    ok_at        timestamptz,
    last_error   text NOT NULL DEFAULT ''
);
