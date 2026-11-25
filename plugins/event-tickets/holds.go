package main

import (
	"encoding/json"
	"errors"
	"time"
)

// A paid cart takes its seats before the buyer pays, and keeps them for
// holdWindow. Every statement here is one call and changes taken in the same
// statement that flips the attendee rows, so the counter cannot drift from the
// rows it counts. The free paths (reserve, reserveMore, releaseOrder) are not
// used for paid carts: they take type after type and compensate on failure.

const holdWindow = "15 minutes"

// holdCode is the code of one ticket of a cart, the type it is for.
type holdCode struct {
	Type int64  `json:"type"`
	Code string `json:"code"`
}

// holdIn is everything holdCart writes. The lines and amounts come from
// priceCart over database prices; nothing in it comes from a price the buyer sent.
type holdIn struct {
	EventID  string
	Buyer    orderIn
	Q        quote
	Codes    []holdCode
	Provider string // stripe, paypal or none (a zero total)
	PayCode  string
}

// holdResult is the answer of holdCart: OrderID is nil when nothing was held.
// Full lists the types that cannot give their seats or are off sale, Changed
// the types whose price is no longer the quoted one or that are not the event's,
// CouponGone a coupon that is no longer valid.
type holdResult struct {
	OrderID    *int64  `json:"order_id"`
	Full       []int64 `json:"full"`
	Changed    []int64 `json:"changed"`
	CouponGone bool    `json:"coupon_gone"`
}

// holdCart takes the seats of every line of a cart, counts the coupon use and
// writes the pending order with its pending attendees, or does nothing, in one
// statement. The ticket types are locked in id order, so two carts never wait
// on each other in a circle, and each is re-checked against the latest
// committed count once its lock is held (rows of an aggregate are all read
// before the update starts). The per-row check in t is the guard; ok only
// keeps the statement all-or-nothing.
func holdCart(h holdIn) (holdResult, error) {
	var res holdResult
	lines, err := json.Marshal(h.Q.Lines)
	if err != nil {
		return res, err
	}
	codes, err := json.Marshal(h.Codes)
	if err != nil {
		return res, err
	}
	answers, err := json.Marshal(h.Buyer.Answers)
	if err != nil {
		return res, err
	}
	var couponID any // NULL without a coupon
	if h.Q.CouponID != 0 {
		couponID = h.Q.CouponID
	}
	err = dbJSON(`WITH want AS (SELECT w."type" AS type_id, w.qty, w.unit
    FROM jsonb_to_recordset($2::jsonb) AS w("type" bigint, qty int, unit bigint)),
 lk AS (SELECT t.id, t.taken, t.capacity, t.on_sale, t.price_cents FROM ticket_types t
    WHERE t.event_id = $1 AND t.id IN (SELECT type_id FROM want) ORDER BY t.id FOR UPDATE),
 cp AS (SELECT c.id FROM coupons c WHERE $8::bigint IS NOT NULL AND c.id = $8::bigint AND c.active
    AND (c.max_uses IS NULL OR c.used < c.max_uses) AND (c.expires_at IS NULL OR c.expires_at > now()) FOR UPDATE),
 ok AS (SELECT (SELECT count(*) FROM lk JOIN want w ON w.type_id = lk.id
        WHERE lk.on_sale AND lk.taken + w.qty <= lk.capacity AND lk.price_cents = w.unit) = (SELECT count(*) FROM want)
    AND ($8::bigint IS NULL OR EXISTS (SELECT 1 FROM cp)) AS go),
 t AS (UPDATE ticket_types tt SET taken = tt.taken + w.qty, touched_at = now()
    FROM want w, ok WHERE ok.go AND tt.id = w.type_id AND tt.event_id = $1
    AND tt.taken + w.qty <= tt.capacity RETURNING tt.id),
 c AS (UPDATE coupons SET used = used + 1 FROM ok
    WHERE ok.go AND coupons.id = $8::bigint AND (SELECT count(*) FROM t) = (SELECT count(*) FROM want) RETURNING coupons.id),
 o AS (INSERT INTO orders (event_id, name, email, answers, status, currency, lines, subtotal_cents, discount_cents,
        fee_cents, fee_absorbed, total_cents, coupon_id, provider, pay_code, expires_at)
    SELECT $1, $4, $5, $6::jsonb, 'pending', $7, $2::jsonb, $9, $10, $11, $12, $13, $8::bigint, $14, $15,
        now() + interval '`+holdWindow+`'
    FROM ok WHERE ok.go AND (SELECT count(*) FROM t) = (SELECT count(*) FROM want) RETURNING id),
 a AS (INSERT INTO attendees (order_id, event_id, type_id, code, status)
    SELECT o.id, $1, (x->>'type')::bigint, x->>'code', 'pending' FROM o, jsonb_array_elements($3::jsonb) AS x RETURNING id)
SELECT jsonb_build_object(
    'order_id', (SELECT id FROM o),
    'full', (SELECT coalesce(jsonb_agg(lk.id ORDER BY lk.id), '[]'::jsonb) FROM lk JOIN want w ON w.type_id = lk.id
        WHERE NOT (lk.on_sale AND lk.taken + w.qty <= lk.capacity)),
    'changed', (SELECT coalesce(jsonb_agg(w.type_id ORDER BY w.type_id), '[]'::jsonb) FROM want w LEFT JOIN lk ON lk.id = w.type_id
        WHERE lk.id IS NULL OR lk.price_cents <> w.unit),
    'coupon_gone', ($8::bigint IS NOT NULL AND NOT EXISTS (SELECT 1 FROM cp)))`,
		&res, h.EventID, string(lines), string(codes), h.Buyer.Name, h.Buyer.Email, string(answers), h.Q.Currency,
		couponID, h.Q.Subtotal, h.Q.Discount, h.Q.Fee, h.Q.FeeAbsorbed, h.Q.Total, h.Provider, h.PayCode)
	if res.Full == nil {
		res.Full = []int64{}
	}
	if res.Changed == nil {
		res.Changed = []int64{}
	}
	return res, err
}

// releaseHold ends a pending order that was not paid, as expired or cancelled:
// its attendees become expired, the seats go back per type from the rows that
// just changed, and the coupon use is given back, in one statement. false when
// the order was not pending any more (it was paid or released meanwhile).
func releaseHold(orderID int64, to string) (bool, error) {
	if to != "expired" && to != "cancelled" {
		return false, errors.New("bad status")
	}
	var done bool
	err := dbJSON(`WITH o AS (UPDATE orders SET status = $2 WHERE id = $1 AND status = 'pending' RETURNING id, coupon_id),
 a AS (UPDATE attendees SET status = 'expired' WHERE order_id IN (SELECT id FROM o) AND status = 'pending' RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = t.taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*)::int AS n FROM a GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id),
 c AS (UPDATE coupons SET used = used - 1 WHERE id IN (SELECT coupon_id FROM o) AND used > 0 RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, orderID, to)
	return done, err
}

// expireHolds releases a batch of pending orders whose hold has lapsed, of one
// event or (empty eventID) of all, in one statement. It answers the Stripe
// sessions of the orders it released, so they can be expired at Stripe too.
func expireHolds(eventID string, limit int) (stripeSessions []string, err error) {
	stripeSessions, _, err = releaseLapsed(eventID, limit)
	return stripeSessions, err
}

// releaseLapsed is expireHolds that also counts the orders it released.
func releaseLapsed(eventID string, limit int) (stripeSessions []string, released int, err error) {
	var res struct {
		Released int      `json:"released"`
		Sessions []string `json:"sessions"`
	}
	err = dbJSON(`WITH o AS (UPDATE orders SET status = 'expired'
    WHERE status = 'pending' AND expires_at < now() AND id IN (SELECT id FROM orders
        WHERE status = 'pending' AND expires_at < now() AND ($1 = '' OR event_id = $1)
        ORDER BY expires_at LIMIT $2 FOR UPDATE SKIP LOCKED)
    RETURNING id, coupon_id, provider, provider_ref),
 a AS (UPDATE attendees SET status = 'expired' WHERE order_id IN (SELECT id FROM o) AND status = 'pending' RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = t.taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*)::int AS n FROM a GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id),
 c AS (UPDATE coupons SET used = greatest(used - x.n, 0)
    FROM (SELECT coupon_id, count(*)::int AS n FROM o WHERE coupon_id IS NOT NULL GROUP BY coupon_id) x
    WHERE coupons.id = x.coupon_id RETURNING coupons.id)
SELECT jsonb_build_object('released', (SELECT count(*) FROM o),
    'sessions', coalesce((SELECT jsonb_agg(provider_ref) FROM o WHERE provider = 'stripe' AND provider_ref IS NOT NULL), '[]'::jsonb))`,
		&res, eventID, limit)
	if res.Sessions == nil {
		res.Sessions = []string{}
	}
	return res.Sessions, res.Released, err
}

// retakeHold takes the seats of an expired or cancelled order back, for a
// payment that arrived after its hold ended: all of them or none, with the same
// lock order as holdCart, and the hold runs again for holdWindow. The coupon
// use is counted again without a limit check: the buyer already paid the price.
func retakeHold(orderID int64) (bool, error) {
	var done bool
	err := dbJSON(`WITH o AS (SELECT id, coupon_id FROM orders WHERE id = $1 AND status IN ('expired', 'cancelled') AND paid_at IS NULL),
 want AS (SELECT a.type_id, count(*)::int AS qty FROM attendees a
    WHERE a.order_id IN (SELECT id FROM o) AND a.status = 'expired' GROUP BY a.type_id),
 lk AS (SELECT t.id, t.taken, t.capacity FROM ticket_types t WHERE t.id IN (SELECT type_id FROM want) ORDER BY t.id FOR UPDATE),
 ok AS (SELECT (SELECT count(*) FROM o) = 1 AND (SELECT count(*) FROM want) > 0
    AND (SELECT count(*) FROM lk JOIN want w ON w.type_id = lk.id WHERE lk.taken + w.qty <= lk.capacity) = (SELECT count(*) FROM want) AS go),
 t AS (UPDATE ticket_types tt SET taken = tt.taken + w.qty, touched_at = now()
    FROM want w, ok WHERE ok.go AND tt.id = w.type_id AND tt.taken + w.qty <= tt.capacity RETURNING tt.id),
 u AS (UPDATE orders SET status = 'pending', expires_at = now() + interval '`+holdWindow+`'
    FROM ok WHERE ok.go AND orders.id = $1 AND orders.status IN ('expired', 'cancelled')
    AND (SELECT count(*) FROM t) = (SELECT count(*) FROM want) RETURNING orders.id),
 a AS (UPDATE attendees SET status = 'pending' WHERE order_id IN (SELECT id FROM u) AND status = 'expired' RETURNING id),
 c AS (UPDATE coupons SET used = used + 1 WHERE id IN (SELECT coupon_id FROM o) AND EXISTS (SELECT 1 FROM u) RETURNING id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM u))`, &done, orderID)
	return done, err
}

// onHoldsExpired is called with the Stripe sessions of released holds, so the
// checkout page can be closed at Stripe. Replaced once Stripe is wired.
var onHoldsExpired = func([]string) {}

// expireHoldsStep releases lapsed holds of every event in batches for at most
// budget; the mail cron runs it before it sends mail.
func expireHoldsStep(budget time.Duration) {
	deadline := now().Add(budget)
	for range expireBatches {
		if !now().Before(deadline) {
			return
		}
		sessions, n, err := releaseLapsed("", expireBatch)
		if err != nil {
			return
		}
		if len(sessions) > 0 {
			onHoldsExpired(sessions)
		}
		if n < expireBatch {
			return
		}
	}
}
