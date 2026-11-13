-- +goose Up
-- Columns the public events route and the calendar widgets read straight from
-- the index: event link, venue slug, address and coordinates, category slugs.
ALTER TABLE events
    ADD COLUMN url            text NOT NULL DEFAULT '',
    ADD COLUMN venue_slug     text NOT NULL DEFAULT '',
    ADD COLUMN venue_address  text NOT NULL DEFAULT '',
    ADD COLUMN venue_lat      double precision,
    ADD COLUMN venue_lon      double precision,
    ADD COLUMN category_slugs jsonb NOT NULL DEFAULT '[]';

-- Rows written before these columns lack the values. Dropping the marker makes
-- readers use the entities until the next reconcile refills the index.
DELETE FROM index_state;
