-- +goose Up
CREATE TABLE downloads_daily (
    media_id uuid NOT NULL,
    day date NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (media_id, day)
);

CREATE TABLE downloads_monthly (
    media_id uuid NOT NULL,
    month date NOT NULL,
    count bigint NOT NULL DEFAULT 0,
    PRIMARY KEY (media_id, month)
);
