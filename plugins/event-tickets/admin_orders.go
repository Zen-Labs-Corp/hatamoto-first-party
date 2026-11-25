package main

import (
	"encoding/json"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// The Orders tab: the list, the refund and the cancel. Refund and cancel move
// money or tickets, so they are for the owner only: core lets the editor role
// reach the route, and the handler checks the role again.

const ordersPerPage = 100

var orderStatuses = []string{"confirmed", "pending", "paid", "expired", "cancelled", "refunded"}

// likePattern turns what the owner typed into a LIKE pattern that matches it
// as plain text anywhere in a name or email.
func likePattern(s string) string {
	r := strings.NewReplacer(`\`, `\\`, `%`, `\%`, `_`, `\_`)
	return "%" + r.Replace(s) + "%"
}

// serveOrders answers GET /admin/orders?status=&event=&q=&cursor=: 100 orders
// per page, newest first. The cursor is the id of the last order of the page.
func serveOrders(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	status, event, q := queryValue(query["status"]), queryValue(query["event"]), queryValue(query["q"])
	if (status != "" && !slices.Contains(orderStatuses, status)) || (event != "" && !idOK(event)) || utf8.RuneCountInString(q) > maxSearch {
		return failReply(http.StatusBadRequest, "bad request")
	}
	var cursor int64
	if c := queryValue(query["cursor"]); c != "" {
		var ok bool
		if cursor, ok = parseID(c); !ok {
			return failReply(http.StatusBadRequest, "bad request")
		}
	}
	pattern := ""
	if q != "" {
		pattern = likePattern(q)
	}
	var rows []json.RawMessage
	err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id DESC), '[]'::jsonb) FROM (
SELECT o.id, o.event_id, coalesce(e.name, '') AS event_name,
  CASE WHEN o.anonymised_at IS NULL THEN o.name ELSE '(removed)' END AS name, o.email, o.status,
  coalesce(o.provider, '') AS provider, o.lines, o.total_cents, o.refunded_cents, coalesce(o.currency, '') AS currency,
  coalesce(o.problem, '') AS problem, o.created_at, o.paid_at,
  (SELECT coalesce(jsonb_agg(jsonb_build_object('id', a.id, 'type_name', t.name, 'status', a.status) ORDER BY a.id), '[]'::jsonb)
     FROM attendees a JOIN ticket_types t ON t.id = a.type_id WHERE a.order_id = o.id) AS tickets
FROM orders o LEFT JOIN events e ON e.entity_id = o.event_id
WHERE ($1 = '' OR o.status = $1) AND ($2 = '' OR o.event_id = $2)
  AND ($3 = '' OR o.name ILIKE $3 ESCAPE '\' OR o.email ILIKE $3 ESCAPE '\')
  AND ($4::bigint = 0 OR o.id < $4::bigint)
ORDER BY o.id DESC LIMIT $5) x`, &rows, status, event, pattern, cursor, ordersPerPage+1)
	if err != nil {
		return unavailable()
	}
	next := ""
	if len(rows) > ordersPerPage {
		rows = rows[:ordersPerPage]
		var last struct {
			ID int64 `json:"id"`
		}
		if json.Unmarshal(rows[ordersPerPage-1], &last) == nil {
			next = strconv.FormatInt(last.ID, 10)
		}
	}
	return jsonReply(http.StatusOK, map[string]any{"orders": rows, "next_cursor": next})
}

// refundBody is the body of POST /admin/orders/{id}/refund. Attendees are the
// tickets a partial refund takes back; a refund of everything left takes back
// every active ticket, whatever is listed.
type refundBody struct {
	Amount    int64   `json:"amount_cents"`
	Attendees []int64 `json:"attendees"`
}

const maxRefundAttendees = 100

// refundable is the order as a refund reads it.
type refundable struct {
	Status     string `json:"status"`
	Provider   string `json:"provider"`
	PayCode    string `json:"pay_code"`
	PaymentRef string `json:"payment_ref"`
	Total      int64  `json:"total_cents"`
	Refunded   int64  `json:"refunded_cents"`
	Currency   string `json:"currency"`
}

func readRefundable(id int64) (*refundable, error) {
	var o *refundable
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('status', status, 'provider', coalesce(provider, ''),
    'pay_code', coalesce(pay_code, ''), 'payment_ref', coalesce(payment_ref, ''), 'total_cents', total_cents, 'refunded_cents', refunded_cents,
    'currency', coalesce(currency, '')) FROM orders WHERE id = $1), 'null'::jsonb)`, &o, id)
	return o, err
}

// serveRefund answers POST /admin/orders/{id}/refund. The provider is asked
// first, with an idempotency key made of the order, the amount already
// refunded and this amount, so a retry after a database failure reuses the
// provider's refund and cannot pay twice. Then one guarded statement records
// it and takes the tickets back.
func serveRefund(r sdk.HTTPRequest, rawID string) sdk.HTTPResponse {
	if r.User == nil || r.User.Role != "owner" {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b refundBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if len(b.Attendees) > maxRefundAttendees || slices.ContainsFunc(b.Attendees, func(a int64) bool { return a < 1 }) {
		return failReply(http.StatusBadRequest, "bad request")
	}
	o, err := readRefundable(id)
	if err != nil {
		return unavailable()
	}
	switch {
	case o == nil:
		return failReply(http.StatusNotFound, "not found")
	case o.Status != "paid" || (o.Provider != "stripe" && o.Provider != "paypal") || o.PaymentRef == "":
		return failReply(http.StatusConflict, "this order cannot be refunded")
	case b.Amount < 1 || b.Amount > o.Total-o.Refunded:
		return failReply(http.StatusBadRequest, "amount must be between 1 and what is left to refund")
	}
	s := loadSettings()
	key := idemKey(o.PayCode, "refund-"+strconv.FormatInt(o.Refunded, 10)+"-"+strconv.FormatInt(b.Amount, 10))
	switch o.Provider {
	case "stripe":
		_, err = stripeRefund(s, o.PaymentRef, b.Amount, key)
	default:
		_, err = paypalRefund(s, o.PaymentRef, b.Amount, o.Currency, key, newDeadline(8*time.Second))
	}
	if err != nil {
		return unavailable()
	}
	which, _ := json.Marshal(b.Attendees)
	full := b.Amount == o.Total-o.Refunded
	var res *struct {
		Status   string `json:"status"`
		Refunded int64  `json:"refunded_cents"`
	}
	err = dbJSON(`WITH o AS (UPDATE orders SET refunded_cents = refunded_cents + $2,
    status = CASE WHEN refunded_cents + $2 = total_cents THEN 'refunded' ELSE status END
    WHERE id = $1 AND status = 'paid' AND refunded_cents = $3 AND refunded_cents + $2 <= total_cents
    RETURNING id, status, refunded_cents),
 a AS (UPDATE attendees SET status = 'refunded' WHERE order_id IN (SELECT id FROM o) AND status = 'active'
    AND ($4::boolean OR id IN (SELECT jsonb_array_elements_text($5::jsonb)::bigint)) RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = t.taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*)::int AS n FROM a GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id)
SELECT coalesce((SELECT jsonb_build_object('status', status, 'refunded_cents', refunded_cents) FROM o), 'null'::jsonb)`,
		&res, id, b.Amount, o.Refunded, full, string(which))
	if err != nil {
		return unavailable()
	}
	if res == nil {
		return failReply(http.StatusConflict, "the order changed, reload it")
	}
	sendNow(kindRefund, id)
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "status": res.Status, "refunded_cents": res.Refunded})
}

// serveCancelOrder answers POST /admin/orders/{id}/cancel. A pending order
// gives its seats back; a paid or free order has its tickets cancelled. No
// money moves: the owner refunds separately.
func serveCancelOrder(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil || u.Role != "owner" {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	done, err := releaseHold(id, "cancelled")
	if err != nil {
		return unavailable()
	}
	if !done {
		err = dbJSON(`WITH o AS (UPDATE orders SET status = 'cancelled' WHERE id = $1 AND status IN ('paid', 'confirmed') RETURNING id),
 a AS (UPDATE attendees SET status = 'cancelled', cancelled_at = now()
    WHERE order_id IN (SELECT id FROM o) AND status = 'active' RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = t.taken - x.n, touched_at = now()
    FROM (SELECT type_id, count(*)::int AS n FROM a GROUP BY type_id) x WHERE t.id = x.type_id RETURNING t.id)
SELECT to_jsonb(EXISTS (SELECT 1 FROM o))`, &done, id)
		if err != nil {
			return unavailable()
		}
	}
	if !done {
		return failReply(http.StatusConflict, "this order cannot be cancelled")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}
