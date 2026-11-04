-- +goose Up
-- One row per confirmed payment. event_id and session_id are unique so a
-- repeated delivery, or a second event for the same checkout, inserts nothing.
-- Only what Stripe confirmed is stored; amount is in minor units.
CREATE TABLE payments (
    id              bigserial PRIMARY KEY,
    event_id        text NOT NULL UNIQUE,
    session_id      text NOT NULL UNIQUE,
    amount          bigint NOT NULL,
    currency        text NOT NULL CHECK (length(currency) = 3),
    customer_email  text NOT NULL DEFAULT '',
    link_label      text NOT NULL,
    link_kind       text NOT NULL CHECK (link_kind IN ('payment', 'donation')),
    payment_link_id text NOT NULL,
    livemode        boolean NOT NULL,
    created         timestamptz NOT NULL,
    received_at     timestamptz NOT NULL DEFAULT now()
);
CREATE INDEX payments_created_idx ON payments (created DESC);
