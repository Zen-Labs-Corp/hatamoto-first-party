-- +goose Up
-- Per import source: what the site holds, where the next manual run continues
-- and the last count of the feed. Written by the owner's Imports tab, never by
-- a run (a run cannot use the database).
CREATE TABLE import_progress (
    source_key   text PRIMARY KEY,
    imported     integer NOT NULL DEFAULT 0,
    cursor_start timestamptz,
    cursor_key   text NOT NULL DEFAULT '',
    found        integer,
    found_capped boolean NOT NULL DEFAULT false,
    remaining    integer,
    counted_at   timestamptz,
    updated_at   timestamptz NOT NULL DEFAULT now()
);
