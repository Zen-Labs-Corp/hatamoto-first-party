-- +goose Up
-- One row per purge request batch. Rows older than 14 days are deleted by the
-- plugin on each insert. The API token is never stored here.
CREATE TABLE purge_log (
  id bigserial PRIMARY KEY,
  at timestamptz NOT NULL DEFAULT now(),
  trigger text NOT NULL,
  provider text NOT NULL,
  urls jsonb NOT NULL DEFAULT '[]'::jsonb,
  status integer NOT NULL DEFAULT 0,
  error text NOT NULL DEFAULT ''
);
CREATE INDEX purge_log_at ON purge_log (at DESC);
