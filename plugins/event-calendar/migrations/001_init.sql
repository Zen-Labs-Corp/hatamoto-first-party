-- +goose Up
-- Event index: one row per published event. Kept in sync by the entity hooks and
-- the hourly reconcile cron; only published events are indexed. ends_at of an
-- all-day event is midnight after the last day.
CREATE TABLE events (
    entity_id       text PRIMARY KEY,
    schema_slug     text NOT NULL,
    slug            text NOT NULL,
    locale          text NOT NULL DEFAULT '',
    name            text NOT NULL,
    status          text NOT NULL DEFAULT 'published' CHECK (status = 'published'),
    starts_at       timestamptz NOT NULL,
    ends_at         timestamptz NOT NULL,
    all_day         boolean NOT NULL DEFAULT false,
    time_zone       text NOT NULL DEFAULT '',
    event_status    text NOT NULL DEFAULT 'scheduled' CHECK (event_status IN ('scheduled', 'cancelled', 'postponed')),
    attendance_mode text NOT NULL DEFAULT 'offline' CHECK (attendance_mode IN ('offline', 'online', 'hybrid')),
    venue_id        text NOT NULL DEFAULT '',
    venue_name      text NOT NULL DEFAULT '',
    venue_city      text NOT NULL DEFAULT '',
    category_ids    jsonb NOT NULL DEFAULT '[]',
    category_names  jsonb NOT NULL DEFAULT '[]',
    series_id       text NOT NULL DEFAULT '',
    price           text NOT NULL DEFAULT '',
    price_amount    double precision,
    featured        boolean NOT NULL DEFAULT false,
    image           text NOT NULL DEFAULT '',
    source_kind     text NOT NULL DEFAULT '',
    source_uid      text NOT NULL DEFAULT '',
    source_url      text NOT NULL DEFAULT '',
    updated_at      timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX events_starts_idx ON events (starts_at);
CREATE INDEX events_slug_idx ON events (schema_slug, slug);
CREATE INDEX events_source_idx ON events (source_kind, source_uid);

-- One row per date an event happens. One per event for now; recurrence expands it.
CREATE TABLE occurrences (
    event_id  text NOT NULL REFERENCES events (entity_id) ON DELETE CASCADE,
    starts_at timestamptz NOT NULL,
    ends_at   timestamptz NOT NULL,
    PRIMARY KEY (event_id, starts_at)
);
CREATE INDEX occurrences_starts_idx ON occurrences (starts_at);
CREATE INDEX occurrences_ends_idx ON occurrences (ends_at);

-- Set once a reconcile has read every event and removed the rows of events that
-- are gone. Readers use the index only after that.
CREATE TABLE index_state (
    id            boolean PRIMARY KEY DEFAULT true CHECK (id),
    reconciled_at timestamptz NOT NULL
);

-- Events dropped by a hook. A reconcile that started earlier must not write
-- them back from the listing it read before the delete.
CREATE TABLE deleted_events (
    entity_id  text PRIMARY KEY,
    deleted_at timestamptz NOT NULL DEFAULT now()
);

-- Address lookups for the map. A failed lookup is retried after a day.
CREATE TABLE geocode_cache (
    address    text PRIMARY KEY,
    latitude   double precision,
    longitude  double precision,
    status     text NOT NULL CHECK (status IN ('ok', 'failed')),
    fetched_at timestamptz NOT NULL DEFAULT now()
);
