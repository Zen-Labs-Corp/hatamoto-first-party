-- +goose Up
-- Recurring events: one occurrence row per date (the table already allows it).
-- repeating marks the events the repeating events page lists, organizer_ids lets
-- the organizer page find an organizer's events, and the series page reads by
-- series_id.
ALTER TABLE events
    ADD COLUMN repeating     boolean NOT NULL DEFAULT false,
    ADD COLUMN organizer_ids jsonb NOT NULL DEFAULT '[]';

CREATE INDEX events_series_idx ON events (series_id);
CREATE INDEX events_repeating_idx ON events (entity_id) WHERE repeating;

-- Rows written before this migration hold one date per event. Dropping the
-- marker makes readers use the entities until the next reconcile refills the
-- index with every date.
DELETE FROM index_state;
