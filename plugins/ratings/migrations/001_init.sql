-- +goose Up
CREATE TABLE votes (
    entity_id uuid        NOT NULL,
    voter     text        NOT NULL,
    stars     smallint    NOT NULL CHECK (stars BETWEEN 1 AND 5),
    created   timestamptz NOT NULL DEFAULT now(),
    updated   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, voter)
);

CREATE TABLE pages (
    entity_id uuid        PRIMARY KEY,
    path      text        NOT NULL,
    updated   timestamptz NOT NULL DEFAULT now()
);
