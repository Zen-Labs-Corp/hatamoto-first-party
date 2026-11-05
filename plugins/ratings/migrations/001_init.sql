-- +goose Up
-- The CHECKs repeat the guest's validation (see validate.go).
CREATE TABLE votes (
    entity_id uuid        NOT NULL,
    voter     text        NOT NULL CHECK (voter ~ '^[0-9a-f]{32}$'),
    stars     smallint    NOT NULL CHECK (stars BETWEEN 1 AND 5),
    created   timestamptz NOT NULL DEFAULT now(),
    updated   timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (entity_id, voter)
);

CREATE TABLE pages (
    entity_id uuid        PRIMARY KEY,
    path      text        NOT NULL CHECK (length(path) <= 512),
    updated   timestamptz NOT NULL DEFAULT now()
);
