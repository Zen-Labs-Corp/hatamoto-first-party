-- +goose Up
-- Own paths of published pages as the host lists them (locale prefix
-- included). entities.get may not resolve a locale-prefixed path, so a link to
-- a published page is recognised from here. Rebuilt by every completed pass.
CREATE TABLE page_paths (
    path      text PRIMARY KEY,
    last_seen timestamptz NOT NULL DEFAULT now()
);
