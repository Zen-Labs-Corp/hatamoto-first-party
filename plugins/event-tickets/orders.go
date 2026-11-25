package main

import (
	"encoding/json"
)

// orderIn is the buyer of an order: personal data, kept until retention ends.
type orderIn struct {
	Name    string
	Email   string
	Answers map[string]string
}

// Capacity is only ever changed by a statement that also checks it: the
// UPDATE of ticket_types takes the seats if taken + n <= capacity, and the
// rows that depend on it are inserted from its RETURNING in the same
// statement. A concurrent buyer waits on the row lock and re-checks the
// latest value, so there is no count-then-insert window.

// reserve takes qty seats of one type for a new order and writes the order and
// its attendees (one per code) in one statement. ok is false, and nothing is
// written, when the type cannot give qty seats.
func reserve(eventID string, typeID int64, o orderIn, codes []string) (orderID int64, ok bool, err error) {
	answers, err := json.Marshal(o.Answers)
	if err != nil {
		return 0, false, err
	}
	codeList, err := json.Marshal(codes)
	if err != nil {
		return 0, false, err
	}
	var id *int64
	err = dbJSON(`WITH t AS (UPDATE ticket_types SET taken = taken + $3, touched_at = now()
    WHERE id = $2 AND event_id = $1 AND on_sale AND taken + $3 <= capacity RETURNING id),
 o AS (INSERT INTO orders (event_id, name, email, answers) SELECT $1, $4, $5, $6::jsonb FROM t RETURNING id),
 a AS (INSERT INTO attendees (order_id, event_id, type_id, code)
    SELECT o.id, $1, $2, c FROM o, jsonb_array_elements_text($7::jsonb) AS c RETURNING id)
SELECT coalesce((SELECT to_jsonb(id) FROM o), 'null'::jsonb)`,
		&id, eventID, typeID, len(codes), o.Name, o.Email, string(answers), string(codeList))
	if err != nil || id == nil {
		return 0, false, err
	}
	return *id, true, nil
}

// reserveMore adds the seats of another type to an order made by reserve.
func reserveMore(orderID int64, eventID string, typeID int64, codes []string) (ok bool, err error) {
	codeList, err := json.Marshal(codes)
	if err != nil {
		return false, err
	}
	var n int
	err = dbJSON(`WITH t AS (UPDATE ticket_types SET taken = taken + $3, touched_at = now()
    WHERE id = $2 AND event_id = $1 AND on_sale AND taken + $3 <= capacity RETURNING id),
 a AS (INSERT INTO attendees (order_id, event_id, type_id, code)
    SELECT $4, $1, $2, c FROM t, jsonb_array_elements_text($5::jsonb) AS c RETURNING id)
SELECT to_jsonb(count(*)) FROM a`,
		&n, eventID, typeID, len(codes), orderID, string(codeList))
	return n > 0, err
}

// releaseOrder undoes an order that could not be completed: its seats go back
// and the order is deleted with its attendees.
func releaseOrder(orderID int64) error {
	if err := dbExec(`WITH c AS (UPDATE attendees SET status = 'cancelled', cancelled_at = now()
    WHERE order_id = $1 AND status = 'active' RETURNING type_id)
UPDATE ticket_types t SET taken = taken - x.n, touched_at = now()
FROM (SELECT type_id, count(*) AS n FROM c GROUP BY type_id) x WHERE t.id = x.type_id`, orderID); err != nil {
		return err
	}
	return dbExec(`DELETE FROM orders WHERE id = $1`, orderID)
}

// eventCounts are the numbers the per-event spam caps look at.
type eventCounts struct {
	ByEmail int `json:"by_email"` // active orders of this address
	Hour    int `json:"hour"`     // orders made in the last hour, not counting holds that lapsed or were cancelled unpaid
	Waiting int `json:"waiting"`  // entries waiting for a seat

	PendingByEmail int `json:"pending_by_email"` // unpaid holds of this address
}

func countEvent(eventID, email string) (eventCounts, error) {
	var c eventCounts
	err := dbJSON(`SELECT jsonb_build_object(
    'by_email', (SELECT count(*) FROM orders o WHERE o.event_id = $1 AND lower(o.email) = lower($2)
        AND EXISTS (SELECT 1 FROM attendees a WHERE a.order_id = o.id AND a.status = 'active')),
    'pending_by_email', (SELECT count(*) FROM orders o WHERE o.event_id = $1 AND lower(o.email) = lower($2) AND o.status = 'pending'),
    'hour', (SELECT count(*) FROM orders WHERE event_id = $1 AND created_at > now() - interval '1 hour'
        AND NOT (status IN ('expired', 'cancelled') AND paid_at IS NULL)),
    'waiting', (SELECT count(*) FROM waitlist WHERE event_id = $1 AND status = 'waiting'))`, &c, eventID, email)
	return c, err
}

// joinWaitlist adds a person to the waitlist of a type; nil when they already
// wait or hold an offer.
func joinWaitlist(eventID string, typeID int64, name, email, code string) (*int64, error) {
	var id *int64
	err := dbJSON(`WITH w AS (INSERT INTO waitlist (event_id, type_id, name, email, code) VALUES ($1, $2, $3, $4, $5)
    ON CONFLICT DO NOTHING RETURNING id)
SELECT coalesce((SELECT to_jsonb(id) FROM w), 'null'::jsonb)`, &id, eventID, typeID, name, email, code)
	return id, err
}
