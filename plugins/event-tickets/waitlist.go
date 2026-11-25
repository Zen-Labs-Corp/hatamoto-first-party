package main

import (
	"net/http"
	"time"

	"hatamoto.dev/sdk"
)

const (
	maxPromotions = 50 // seats offered per call
	offerWindow   = "48 hours"
)

// A seat is held for an offer by the same statement that makes the offer: the
// ticket type's counter is raised in the statement that picks the oldest
// waiting entry, so an offer never exists without its seat and the seat is
// never given twice. Claiming turns the offer into an order and leaves the
// counter alone: the seat is already counted.

// eventOpenSQL is a SQL condition: the snapshot of the event whose id is eventCol
// is not known to be closed. Closed means not published (cancelled, postponed,
// unpublished, deleted) or over. An event without a snapshot is not closed.
func eventOpenSQL(eventCol string) string {
	return `NOT EXISTS (SELECT 1 FROM events ev WHERE ev.entity_id = ` + eventCol + `
    AND NOT coalesce(ev.status = 'published' AND coalesce(ev.ends_at, ev.starts_at) > now(), false))`
}

// eventOverSQL is a SQL condition: the event whose id is eventCol is over,
// cancelled or deleted for good. Unpublished and postponed events are only
// paused (see eventOpenSQL), so their waitlists are kept.
func eventOverSQL(eventCol string) string {
	return `EXISTS (SELECT 1 FROM events ev WHERE ev.entity_id = ` + eventCol + `
    AND (ev.status IN ('cancelled', 'deleted') OR coalesce(ev.ends_at, ev.starts_at) <= now()))`
}

// promoteNext offers one freed seat of a type to the oldest waiting entry and
// queues the offer mail. ok is false when nobody waits or no seat is free.
func promoteNext(typeID int64) (waitID int64, ok bool) {
	var id *int64
	err := dbJSON(`WITH w AS (SELECT id FROM waitlist WHERE type_id = $1 AND status = 'waiting' ORDER BY id LIMIT 1 FOR UPDATE SKIP LOCKED),
 t AS (UPDATE ticket_types SET taken = taken + 1, touched_at = now()
    WHERE id = $1 AND on_sale AND price_cents = 0 AND taken + 1 <= capacity AND EXISTS (SELECT 1 FROM w)
    AND `+eventOpenSQL("ticket_types.event_id")+` RETURNING id),
 u AS (UPDATE waitlist SET status = 'offered', offered_at = now(), expires_at = now() + interval '`+offerWindow+`'
    WHERE id = (SELECT id FROM w) AND EXISTS (SELECT 1 FROM t) RETURNING id)
SELECT coalesce((SELECT to_jsonb(id) FROM u), 'null'::jsonb)`, &id, typeID)
	if err != nil || id == nil {
		return 0, false
	}
	// The seat is held. If the mail cannot be queued the offer lapses after
	// the window and the seat goes to the next person.
	_ = enqueue(kindWaitlistOffer, *id)
	drainMail(2*time.Second, 5)
	return *id, true
}

// promoteAll offers seats until nobody waits or none is free.
func promoteAll(typeID int64) { promoteCount(typeID) }

// promoteCount is promoteAll that says how many seats it offered.
func promoteCount(typeID int64) int {
	n := 0
	for range maxPromotions {
		if _, ok := promoteNext(typeID); !ok {
			break
		}
		n++
	}
	return n
}

// cancelAttendee cancels an active ticket and frees its seat, then offers the
// seat to the waitlist; promoted is how many seats it offered. ok is false
// when the ticket is not active.
func cancelAttendee(id int64) (typeID int64, promoted int, ok bool) {
	var tid *int64
	err := dbJSON(`WITH a AS (UPDATE attendees SET status = 'cancelled', cancelled_at = now()
    WHERE id = $1 AND status = 'active' RETURNING type_id),
 t AS (UPDATE ticket_types t SET taken = taken - 1, touched_at = now() FROM a WHERE t.id = a.type_id RETURNING t.id)
SELECT coalesce((SELECT to_jsonb(id) FROM t), 'null'::jsonb)`, &tid, id)
	if err != nil || tid == nil {
		return 0, 0, false
	}
	return *tid, promoteCount(*tid), true
}

// waitEntry is a waitlist entry as its pages read it.
type waitEntry struct {
	Status     string `json:"status"`
	EventID    string `json:"event_id"`
	EventName  string `json:"event_name"`
	TypeName   string `json:"type_name"`
	Expired    bool   `json:"expired"`     // the offer window is over
	TicketCode string `json:"ticket_code"` // of the order a claim made
}

func lookupWait(code string) (*waitEntry, error) {
	var w *waitEntry
	err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('status', w.status, 'event_id', w.event_id,
    'event_name', coalesce(e.name, ''), 'type_name', t.name,
    'expired', ((w.expires_at IS NOT NULL AND w.expires_at <= now()) OR NOT (`+eventOpenSQL("w.event_id")+`)),
    'ticket_code', coalesce((SELECT a.code FROM orders o JOIN attendees a ON a.order_id = o.id
        WHERE o.waitlist_id = w.id ORDER BY a.id LIMIT 1), ''))
FROM waitlist w JOIN ticket_types t ON t.id = w.type_id LEFT JOIN events e ON e.entity_id = w.event_id
WHERE w.code = $1), 'null'::jsonb)`, &w, code)
	return w, err
}

// claimOffer turns a live offer into an order with one ticket. It returns the
// order and the ticket code, or ok false when the offer is not live.
func claimOffer(waitCode, ticketCode string) (orderID int64, ok bool, err error) {
	var res *struct {
		OrderID int64 `json:"order_id"`
	}
	err = dbJSON(`WITH w AS (UPDATE waitlist SET status = 'claimed' WHERE code = $1 AND status = 'offered' AND expires_at > now() AND `+eventOpenSQL("waitlist.event_id")+`
    AND EXISTS (SELECT 1 FROM ticket_types t WHERE t.id = waitlist.type_id AND t.price_cents = 0)
    RETURNING id, event_id, type_id, name, email),
 o AS (INSERT INTO orders (event_id, name, email, waitlist_id) SELECT event_id, name, email, id FROM w RETURNING id, event_id),
 a AS (INSERT INTO attendees (order_id, event_id, type_id, code) SELECT o.id, o.event_id, w.type_id, $2 FROM o, w RETURNING code)
SELECT coalesce((SELECT jsonb_build_object('order_id', o.id, 'code', a.code) FROM o, a), 'null'::jsonb)`, &res, waitCode, ticketCode)
	if err != nil || res == nil {
		return 0, false, err
	}
	return res.OrderID, true, nil
}

// leaveWaitlist ends a waiting entry or an offer. An offer holds a seat, which
// goes back in the same statement (RETURNING sees the new status, so an offer
// is told by offered_at, which only an offer sets); freed is that type, 0 when none was held.
func leaveWaitlist(waitCode string) (freed int64, err error) {
	var tid *int64
	err = dbJSON(`WITH w AS (UPDATE waitlist SET status = 'left' WHERE code = $1 AND status IN ('waiting', 'offered')
    RETURNING type_id, offered_at IS NOT NULL AS held),
 t AS (UPDATE ticket_types t SET taken = taken - 1, touched_at = now() FROM w WHERE t.id = w.type_id AND w.held RETURNING t.id)
SELECT coalesce((SELECT to_jsonb(id) FROM t), 'null'::jsonb)`, &tid, waitCode)
	if tid != nil {
		return *tid, err
	}
	return 0, err
}

// waitContext verifies the token and reads the entry; ok is false for any
// forged, unknown or unreadable one.
func waitContext(tok string) (key []byte, code string, w *waitEntry, ok bool) {
	key, err := hmacSecret()
	if err != nil {
		return nil, "", nil, false
	}
	code, valid := verifyToken(key, kindWait, tok)
	if !valid {
		return nil, "", nil, false
	}
	w, err = lookupWait(code)
	if err != nil || w == nil {
		return nil, "", nil, false
	}
	if name, found, err := readEvent(w.EventID, loadSettings()); err == nil && found && name.Name != "" {
		w.EventName = name.Name
	}
	return key, code, w, true
}

func (w *waitEntry) subject() string {
	if w.EventName == "" {
		return w.TypeName
	}
	return w.EventName + ", " + w.TypeName
}

func waitPath(tok, action string) string { return "/p/event-tickets/waitlist/" + tok + "/" + action }

func readyPage(key []byte, ticketCode string) sdk.HTTPResponse {
	return htmlPage(http.StatusOK, pageData{
		Title: "Your ticket is ready",
		Lines: []string{"Your ticket is confirmed. We also sent it to you by email."},
		Links: []pageLink{{ticketPath(signCode(key, kindTicket, ticketCode)), "Open your ticket"}},
	})
}

func expiredPage() sdk.HTTPResponse {
	return htmlPage(http.StatusOK, pageData{Title: "This offer has expired", Lines: []string{"The ticket was offered to the next person on the waitlist."}})
}

func leftPage() sdk.HTTPResponse {
	return htmlPage(http.StatusOK, pageData{Title: "You left the waitlist", Lines: []string{"We will not email you about this event again."}})
}

// serveClaim is the page an offer mail links to. Opening it changes nothing
// (mail scanners open links); the confirm link, with yes=1, claims.
func serveClaim(tok string, query map[string][]string) sdk.HTTPResponse {
	key, code, w, ok := waitContext(tok)
	if !ok {
		return notFound()
	}
	yes := first(query["yes"]) == "1"
	if w.Status == "offered" && !w.Expired && yes {
		ticketCode, err := newCode()
		if err != nil {
			return plain(http.StatusServiceUnavailable, "unavailable")
		}
		orderID, claimed, err := claimOffer(code, ticketCode)
		if err != nil {
			return plain(http.StatusServiceUnavailable, "unavailable")
		}
		if claimed {
			sendNow(kindConfirm, orderID)
			return readyPage(key, ticketCode)
		}
		// Lost a race (the same link opened twice, or the offer ended): show what is true now.
		if w, err = lookupWait(code); err != nil || w == nil {
			return notFound()
		}
	}
	switch {
	case w.Status == "claimed" && w.TicketCode != "":
		return readyPage(key, w.TicketCode)
	case w.Status == "offered" && !w.Expired:
		return htmlPage(http.StatusOK, pageData{
			Title: "Claim your ticket",
			Lines: []string{"Do you want the ticket for " + w.subject() + "?"},
			Links: []pageLink{{waitPath(tok, "claim") + "?yes=1", "Yes, claim it"}, {waitPath(tok, "leave"), "No, leave the waitlist"}},
		})
	case w.Status == "waiting":
		return htmlPage(http.StatusOK, pageData{
			Title: "You are still on the waitlist",
			Lines: []string{"There is no ticket for you yet for " + w.subject() + ". We will email you if one becomes available."},
			Links: []pageLink{{waitPath(tok, "leave"), "Leave the waitlist"}},
		})
	case w.Status == "left":
		return leftPage()
	}
	return expiredPage()
}

// serveLeave is the page the leave link of a waitlist mail opens; like claim
// it only acts on yes=1.
func serveLeave(tok string, query map[string][]string) sdk.HTTPResponse {
	key, code, w, ok := waitContext(tok)
	if !ok {
		return notFound()
	}
	switch w.Status {
	case "claimed":
		if w.TicketCode != "" {
			return readyPage(key, w.TicketCode)
		}
	case "waiting", "offered":
		if first(query["yes"]) != "1" {
			return htmlPage(http.StatusOK, pageData{
				Title: "Leave the waitlist?",
				Lines: []string{"You will not get a ticket for " + w.subject() + "."},
				Links: []pageLink{{waitPath(tok, "leave") + "?yes=1", "Yes, leave the waitlist"}},
			})
		}
		freed, err := leaveWaitlist(code)
		if err != nil {
			return plain(http.StatusServiceUnavailable, "unavailable")
		}
		if freed != 0 {
			promoteAll(freed)
		}
		return leftPage()
	case "left":
		return leftPage()
	}
	return expiredPage()
}
