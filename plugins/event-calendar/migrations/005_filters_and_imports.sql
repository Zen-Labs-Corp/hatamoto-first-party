-- +goose Up
-- Tags and site-added fields for the calendar filters, and when the index first saw an event
-- (the Imports tab counts new imported events from it). Existing rows keep first_seen NULL.
ALTER TABLE events
    ADD COLUMN tags       jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN extra      jsonb NOT NULL DEFAULT '{}',
    ADD COLUMN first_seen timestamptz;
ALTER TABLE events ALTER COLUMN first_seen SET DEFAULT now();
CREATE INDEX events_first_seen_idx ON events (source_kind, first_seen) WHERE first_seen IS NOT NULL;

-- Rows written before this migration hold no tags or fields. Dropping the
-- marker makes readers use the entities until the next reconcile refills the
-- index.
DELETE FROM index_state;
