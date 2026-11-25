-- +goose Up
-- Seating maps: grid sections (rows x seats) and GA zones. Maps are templates;
-- a ticket type is bound to one section of one map per event.
CREATE TABLE seat_maps (
    id         bigserial PRIMARY KEY,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    created_at timestamptz NOT NULL DEFAULT now()
);

CREATE TABLE map_sections (
    id           bigserial PRIMARY KEY,
    map_id       bigint NOT NULL REFERENCES seat_maps (id) ON DELETE CASCADE,
    kind         text NOT NULL CHECK (kind IN ('grid', 'zone')),
    name         text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    tier         text NOT NULL DEFAULT '' CHECK (length(tier) <= 40),
    position     int NOT NULL DEFAULT 0,
    row_labels   jsonb NOT NULL DEFAULT '[]',
    seats_per_row int NOT NULL DEFAULT 0 CHECK (seats_per_row BETWEEN 0 AND 100),
    capacity     int NOT NULL DEFAULT 0 CHECK (capacity BETWEEN 0 AND 100000),
    CHECK ((kind = 'grid' AND seats_per_row >= 1) OR (kind = 'zone' AND capacity >= 1))
);
CREATE INDEX map_sections_map_idx ON map_sections (map_id, position);

CREATE TABLE seats (
    id         bigserial PRIMARY KEY,
    section_id bigint NOT NULL REFERENCES map_sections (id) ON DELETE CASCADE,
    row_idx    int NOT NULL CHECK (row_idx >= 0),
    row_label  text NOT NULL,
    num        int NOT NULL CHECK (num >= 1),
    blocked    boolean NOT NULL DEFAULT false,
    UNIQUE (section_id, row_idx, num)
);

ALTER TABLE ticket_types ADD COLUMN section_id bigint REFERENCES map_sections (id);
CREATE UNIQUE INDEX ticket_types_section_idx ON ticket_types (event_id, section_id) WHERE section_id IS NOT NULL;

ALTER TABLE attendees ADD COLUMN seat_id bigint REFERENCES seats (id),
    ADD COLUMN seat_label text CHECK (seat_label IS NULL OR length(seat_label) BETWEEN 1 AND 120);

-- The double-sale guard: one live ticket per seat and event.
CREATE UNIQUE INDEX attendees_seat_live_idx ON attendees (event_id, seat_id)
    WHERE seat_id IS NOT NULL AND status IN ('pending', 'active');
-- Deleting a seat checks its tickets by seat alone.
CREATE INDEX attendees_seat_idx ON attendees (seat_id) WHERE seat_id IS NOT NULL;

-- Seats picked but not bought yet. No personal data. One row per seat and
-- event: the double-hold guard. event_id is an entity id, no FK.
CREATE TABLE seat_holds (
    id         bigserial PRIMARY KEY,
    event_id   text NOT NULL,
    seat_id    bigint NOT NULL REFERENCES seats (id) ON DELETE CASCADE,
    code       text NOT NULL,
    expires_at timestamptz NOT NULL,
    created_at timestamptz NOT NULL DEFAULT now(),
    UNIQUE (event_id, seat_id)
);
CREATE INDEX seat_holds_code_idx ON seat_holds (code);
CREATE INDEX seat_holds_expires_idx ON seat_holds (expires_at);
