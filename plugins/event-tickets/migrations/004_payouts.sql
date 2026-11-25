-- +goose Up
-- Payouts: who gets which share of an event's ticket money, and which months were paid out by hand.
CREATE TABLE split_rules (
    id          bigserial PRIMARY KEY,
    event_id    text NOT NULL,              -- entity id, no FK (events live in another plugin)
    position    int NOT NULL,
    payee_name  text NOT NULL,
    payee_email text NOT NULL,              -- payout email as entered; grouped by lower()
    kind        text NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value       bigint NOT NULL CHECK (value > 0), -- basis points (percent) or minor units per ticket (fixed)
    created_at  timestamptz NOT NULL DEFAULT now(),
    CHECK (kind <> 'percent' OR value <= 10000)
);
CREATE UNIQUE INDEX split_rules_event_idx ON split_rules (event_id, position);
CREATE TABLE payouts (
    id          bigserial PRIMARY KEY,
    period      text NOT NULL CHECK (period ~ '^[0-9]{4}-(0[1-9]|1[0-2])$'), -- UTC month of orders.paid_at
    payee_email text NOT NULL,              -- lower-cased
    payee_name  text NOT NULL DEFAULT '',
    currency    text NOT NULL CHECK (currency ~ '^[A-Z]{3}$'),
    owed_cents  bigint NOT NULL CHECK (owed_cents > 0), -- snapshot at mark time
    paid_on     date NOT NULL,
    reference   text NOT NULL DEFAULT '',
    marked_by   text NOT NULL DEFAULT '',
    created_at  timestamptz NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX payouts_key_idx ON payouts (period, payee_email, currency);
CREATE INDEX orders_paid_idx ON orders (paid_at) WHERE paid_at IS NOT NULL;
CREATE INDEX attendees_order_idx ON attendees (order_id);
