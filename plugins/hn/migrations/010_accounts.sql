-- +goose Up
-- Accounts (phase: accounts). One row per signed-in commenter.

CREATE TABLE users (
    id bigint GENERATED ALWAYS AS IDENTITY PRIMARY KEY,
    commenter_key text NOT NULL UNIQUE,
    username text NOT NULL,
    username_lower text NOT NULL UNIQUE,
    about text NOT NULL DEFAULT '',
    karma integer NOT NULL DEFAULT 1,
    showdead boolean NOT NULL DEFAULT false,
    noprocrast boolean NOT NULL DEFAULT false,
    maxvisit integer NOT NULL DEFAULT 20,
    minaway integer NOT NULL DEFAULT 180,
    created_at timestamptz NOT NULL DEFAULT now()
);
