-- +goose Up
-- Plugin secrets generated once (the signing key of ticket links and form tokens).
CREATE TABLE meta (
    key   text PRIMARY KEY,
    value text NOT NULL
);

-- A snapshot of the dates of events that have ticket types, so the hourly job
-- can tell which events ended without reading them all. No event text beyond
-- the name is kept.
CREATE TABLE events (
    entity_id   text PRIMARY KEY,
    schema_slug text,
    name        text NOT NULL DEFAULT '',
    starts_at   timestamptz,
    ends_at     timestamptz,
    status      text NOT NULL DEFAULT 'published',
    seen_at     timestamptz NOT NULL DEFAULT now()
);

-- One ticket pool per event. taken is the counter capacity is enforced on:
-- it only ever changes in the same statement that checks it against capacity.
CREATE TABLE ticket_types (
    id         bigserial PRIMARY KEY,
    event_id   text NOT NULL,
    name       text NOT NULL CHECK (length(name) BETWEEN 1 AND 80),
    capacity   int NOT NULL CHECK (capacity BETWEEN 1 AND 100000),
    taken      int NOT NULL DEFAULT 0 CHECK (taken >= 0),
    waitlist   boolean NOT NULL DEFAULT true,
    on_sale    boolean NOT NULL DEFAULT true,
    position   int NOT NULL DEFAULT 0,
    touched_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX ticket_types_event_idx ON ticket_types (event_id);

-- Custom questions of an event, answered once per order.
CREATE TABLE fields (
    event_id text NOT NULL,
    key      text NOT NULL CHECK (key ~ '^[a-z][a-z0-9_]{0,31}$'),
    position int NOT NULL DEFAULT 0,
    label    text NOT NULL,
    kind     text NOT NULL CHECK (kind IN ('text', 'select', 'checkbox')),
    options  jsonb NOT NULL DEFAULT '[]',
    required boolean NOT NULL DEFAULT false,
    PRIMARY KEY (event_id, key)
);

-- One RSVP. Holds the attendee's name, email and answers: personal data,
-- erased (anonymised_at set, name, email and answers blanked) by the hourly
-- job once the event ended more than retention_months ago.
CREATE TABLE orders (
    id           bigserial PRIMARY KEY,
    event_id     text NOT NULL,
    name         text NOT NULL,
    email        text NOT NULL,
    answers      jsonb NOT NULL DEFAULT '{}',
    created_at   timestamptz NOT NULL DEFAULT now(),
    mailed_at    timestamptz,
    resent_at    timestamptz,
    resend_count int NOT NULL DEFAULT 0,
    waitlist_id  bigint UNIQUE,
    anonymised_at timestamptz
);
CREATE INDEX orders_event_idx ON orders (event_id, created_at);
CREATE INDEX orders_email_idx ON orders (event_id, lower(email));

-- One ticket. code is the 128-bit random part of the ticket link.
CREATE TABLE attendees (
    id           bigserial PRIMARY KEY,
    order_id     bigint NOT NULL REFERENCES orders (id) ON DELETE CASCADE,
    event_id     text NOT NULL,
    type_id      bigint NOT NULL REFERENCES ticket_types (id),
    code         text NOT NULL UNIQUE,
    status       text NOT NULL DEFAULT 'active' CHECK (status IN ('active', 'cancelled')),
    created_at   timestamptz NOT NULL DEFAULT now(),
    cancelled_at timestamptz
);
CREATE INDEX attendees_event_idx ON attendees (event_id);
CREATE INDEX attendees_type_idx ON attendees (type_id, status);

-- People waiting for a seat of a sold-out type. Personal data like orders;
-- erased with them.
CREATE TABLE waitlist (
    id         bigserial PRIMARY KEY,
    event_id   text NOT NULL,
    type_id    bigint NOT NULL REFERENCES ticket_types (id),
    name       text NOT NULL,
    email      text NOT NULL,
    code       text NOT NULL UNIQUE,
    status     text NOT NULL DEFAULT 'waiting' CHECK (status IN ('waiting', 'offered', 'claimed', 'left', 'expired')),
    created_at timestamptz NOT NULL DEFAULT now(),
    offered_at timestamptz,
    expires_at timestamptz
);
-- The same person waits once per type.
CREATE UNIQUE INDEX waitlist_open_idx ON waitlist (type_id, lower(email))
    WHERE status IN ('waiting', 'offered');
CREATE INDEX waitlist_queue_idx ON waitlist (type_id, status, id);

-- Mails still to send. Holds ids only, never an address: the mail is built
-- from the order or waitlist row when it is sent.
CREATE TABLE mail_queue (
    id         bigserial PRIMARY KEY,
    kind       text NOT NULL CHECK (kind IN ('confirm', 'waitlist_joined', 'waitlist_offer')),
    ref_id     bigint NOT NULL,
    attempts   int NOT NULL DEFAULT 0,
    next_at    timestamptz NOT NULL DEFAULT now(),
    created_at timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX mail_queue_next_idx ON mail_queue (next_at, id);
