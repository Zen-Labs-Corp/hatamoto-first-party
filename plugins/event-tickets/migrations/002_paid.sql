-- +goose Up
-- Paid tickets: prices on ticket types, payment state on orders, coupons,
-- bundle rules and the ids of payment events already handled.
-- No card data is ever stored: only amounts, currency and provider ids.
ALTER TABLE ticket_types ADD COLUMN price_cents bigint NOT NULL DEFAULT 0 CHECK (price_cents BETWEEN 0 AND 99999999);

-- Existing orders are free ones: they become 'confirmed'.
ALTER TABLE orders
    ADD COLUMN status text NOT NULL DEFAULT 'confirmed'
        CHECK (status IN ('confirmed', 'pending', 'paid', 'expired', 'cancelled', 'refunded')),
    ADD COLUMN currency text CHECK (currency IS NULL OR currency ~ '^[A-Z]{3}$'),
    ADD COLUMN lines jsonb NOT NULL DEFAULT '[]',
    ADD COLUMN subtotal_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN discount_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN fee_cents bigint NOT NULL DEFAULT 0,
    ADD COLUMN fee_absorbed boolean NOT NULL DEFAULT false,
    ADD COLUMN total_cents bigint NOT NULL DEFAULT 0 CHECK (total_cents >= 0),
    ADD COLUMN refunded_cents bigint NOT NULL DEFAULT 0 CHECK (refunded_cents >= 0 AND refunded_cents <= total_cents),
    ADD COLUMN coupon_id bigint,
    ADD COLUMN provider text CHECK (provider IS NULL OR provider IN ('stripe', 'paypal', 'none')),
    ADD COLUMN provider_ref text,
    ADD COLUMN payment_ref text,
    ADD COLUMN pay_code text UNIQUE,
    ADD COLUMN expires_at timestamptz,
    ADD COLUMN paid_at timestamptz,
    ADD COLUMN refund_claimed_at timestamptz,
    ADD COLUMN problem text CHECK (problem IS NULL OR problem IN ('amount_mismatch', 'refund_failed'));
-- orders.lines: snapshot at hold time, [{type, name, qty, unit}].
-- orders.provider_ref: Stripe session id or PayPal order id.
-- orders.payment_ref: Stripe payment intent or PayPal capture id.
-- orders.refund_claimed_at: a refund is in flight; set before the provider is asked, so two refunds cannot both go out.
-- orders.pay_code: 128-bit random, signed into the return and cancel links.
CREATE UNIQUE INDEX orders_provider_ref_idx ON orders (provider, provider_ref) WHERE provider_ref IS NOT NULL;
CREATE INDEX orders_pending_idx ON orders (expires_at) WHERE status = 'pending';
CREATE INDEX orders_status_idx ON orders (status, created_at);

ALTER TABLE attendees DROP CONSTRAINT attendees_status_check;
ALTER TABLE attendees ADD CONSTRAINT attendees_status_check
    CHECK (status IN ('active', 'cancelled', 'pending', 'expired', 'refunded'));
ALTER TABLE mail_queue DROP CONSTRAINT mail_queue_kind_check;
ALTER TABLE mail_queue ADD CONSTRAINT mail_queue_kind_check
    CHECK (kind IN ('confirm', 'waitlist_joined', 'waitlist_offer', 'refund'));

-- Discount codes. code is upper-case; event_id and type_id empty = any.
CREATE TABLE coupons (
    id         bigserial PRIMARY KEY,
    code       text NOT NULL CHECK (code ~ '^[A-Z0-9_-]{3,32}$'),
    kind       text NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value      bigint NOT NULL CHECK (value > 0),
    event_id   text,
    type_id    bigint REFERENCES ticket_types (id) ON DELETE CASCADE,
    max_uses   int CHECK (max_uses IS NULL OR max_uses > 0),
    used       int NOT NULL DEFAULT 0 CHECK (used >= 0),
    expires_at timestamptz,
    active     boolean NOT NULL DEFAULT true,
    created_at timestamptz NOT NULL DEFAULT now(),
    CHECK (kind <> 'percent' OR value <= 100)
);
CREATE UNIQUE INDEX coupons_code_idx ON coupons (code);

-- Bundle discounts of an event. type_ids empty = any type of the event;
-- all_types = every listed type must be in the cart (a combo).
CREATE TABLE bundle_rules (
    id        bigserial PRIMARY KEY,
    event_id  text NOT NULL,
    type_ids  jsonb NOT NULL DEFAULT '[]',
    all_types boolean NOT NULL DEFAULT false,
    min_qty   int NOT NULL CHECK (min_qty BETWEEN 1 AND 10),
    kind      text NOT NULL CHECK (kind IN ('percent', 'fixed')),
    value     bigint NOT NULL CHECK (value > 0),
    position  int NOT NULL DEFAULT 0,
    CHECK (kind <> 'percent' OR value <= 100)
);
CREATE INDEX bundle_rules_event_idx ON bundle_rules (event_id);

-- Payment events already handled, so a replayed event does nothing twice.
CREATE TABLE payment_events (
    provider    text NOT NULL,
    event_id    text NOT NULL,
    order_id    bigint,
    outcome     text NOT NULL,
    received_at timestamptz NOT NULL DEFAULT now(),
    PRIMARY KEY (provider, event_id)
);
