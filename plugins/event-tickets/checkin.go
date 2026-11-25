package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// scanMax is the longest scan text the route takes (a ticket URL is far shorter).
const scanMax = 512

// Search: a query of 2 to 64 characters matches names; one that looks like the
// start of a ticket code (4 to 32 hex digits) matches codes too.
const (
	searchMin     = 2
	searchMax     = 64
	searchLimit   = 20
	checkinNotYet = "not checked in"
)

var codePrefixRe = regexp.MustCompile(`^[0-9a-f]{4,32}$`)

// checkinBody is the body of a scan: exactly one of the two fields is set.
type checkinBody struct {
	Scan       string `json:"scan"`
	AttendeeID int64  `json:"attendee_id"`
}

// checkedIn is what a scan shows of the ticket it let in or found already in.
type checkedIn struct {
	ID          int64      `json:"id"`
	Name        string     `json:"name"`
	TypeName    string     `json:"type_name"`
	CheckedInAt *time.Time `json:"checked_in_at"`
	CheckedInBy *string    `json:"checked_in_by"`
}

// ticketState is the row a scan that changed nothing looks at to say why.
type ticketState struct {
	checkedIn
	EventID    string `json:"event_id"`
	Status     string `json:"status"`
	Anonymised bool   `json:"anonymised"`
}

// scanToken is the ticket token in what a QR code holds: the part after the
// last "/ticket/" of a ticket URL, without query or fragment, or the text itself.
func scanToken(s string) string {
	if i := strings.LastIndex(s, "/ticket/"); i >= 0 {
		s = s[i+len("/ticket/"):]
	}
	if i := strings.IndexAny(s, "?#"); i >= 0 {
		s = s[:i]
	}
	return strings.TrimSpace(s)
}

// refused is a final answer about a ticket, not a fault of the request: it is
// a 200, so a queued scan is never retried because of it.
func refused(reason string) sdk.HTTPResponse {
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "result": "refused", "reason": reason})
}

func scanAnswer(result string, a checkedIn) sdk.HTTPResponse {
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "result": result, "attendee": a})
}

// serveCheckinState answers GET /admin/events/{id}/checkin?q=: how many active
// tickets are checked in, and for a query of 2 to 64 characters the tickets
// that match it by name or code.
func serveCheckinState(u *sdk.HTTPUser, eventID string, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var counts struct {
		Checked int `json:"checked"`
		Total   int `json:"total"`
	}
	err := dbJSON(`SELECT jsonb_build_object('checked', count(*) FILTER (WHERE checked_in_at IS NOT NULL), 'total', count(*))
FROM attendees WHERE event_id = $1 AND status = 'active'`, &counts, eventID)
	if err != nil {
		return unavailable()
	}
	matches := []json.RawMessage{}
	if q := strings.TrimSpace(queryValue(query["q"])); utf8.RuneCountInString(q) >= searchMin && utf8.RuneCountInString(q) <= searchMax {
		byCode := ""
		args := []any{eventID, q, searchLimit}
		if codePrefixRe.MatchString(q) {
			byCode = " OR a.code LIKE $4 || '%'"
			args = append(args, q)
		}
		err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.name, x.id), '[]'::jsonb) FROM (
SELECT a.id, CASE WHEN o.anonymised_at IS NULL THEN o.name ELSE '(removed)' END AS name,
  t.name AS type_name, a.status, a.checked_in_at, a.checked_in_by
FROM attendees a JOIN orders o ON o.id = a.order_id JOIN ticket_types t ON t.id = a.type_id
WHERE a.event_id = $1 AND ((o.anonymised_at IS NULL AND strpos(lower(o.name), lower($2)) > 0)`+byCode+`)
ORDER BY o.name, a.id LIMIT $3) x`, &matches, args...)
		if err != nil {
			return unavailable()
		}
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "checked": counts.Checked, "total": counts.Total, "matches": matches})
}

// serveCheckinScan answers POST /admin/events/{id}/checkin with {"scan": text}
// (what a QR code holds) or {"attendee_id": n} (a manual check-in from search).
// A good body always gets a 200 with result ok, already or refused. The UPDATE
// is the guard, so two scans of one ticket at once give one ok and one already.
func serveCheckinScan(r sdk.HTTPRequest, eventID string) sdk.HTTPResponse {
	u := r.User
	if u == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b checkinBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	if (b.Scan != "") == (b.AttendeeID != 0) || len(b.Scan) > scanMax || b.AttendeeID < 0 {
		return failReply(http.StatusBadRequest, "bad request")
	}
	where, key := "a.id = $1", any(b.AttendeeID)
	if b.Scan != "" {
		secret, err := hmacSecret()
		if err != nil {
			return unavailable()
		}
		code, valid := verifyToken(secret, kindTicket, scanToken(b.Scan))
		if !valid {
			return refused("invalid")
		}
		where, key = "a.code = $1", code
	}
	var done *checkedIn
	err := dbJSON(`WITH u AS (UPDATE attendees a SET checked_in_at = now(), checked_in_by = $3 FROM orders o
WHERE `+where+` AND a.event_id = $2 AND a.status = 'active' AND a.checked_in_at IS NULL
  AND o.id = a.order_id AND o.anonymised_at IS NULL
RETURNING a.id, o.name, (SELECT t.name FROM ticket_types t WHERE t.id = a.type_id) AS type_name, a.checked_in_at, a.checked_in_by)
SELECT coalesce((SELECT to_jsonb(u) FROM u), 'null'::jsonb)`, &done, key, eventID, u.ID)
	if err != nil {
		return unavailable()
	}
	if done != nil {
		return scanAnswer("ok", *done)
	}
	var s *ticketState
	err = dbJSON(`SELECT coalesce((SELECT to_jsonb(x) FROM (
SELECT a.id, CASE WHEN o.anonymised_at IS NULL THEN o.name ELSE '(removed)' END AS name, t.name AS type_name,
  a.checked_in_at, a.checked_in_by, a.event_id, a.status, o.anonymised_at IS NOT NULL AS anonymised
FROM attendees a JOIN orders o ON o.id = a.order_id JOIN ticket_types t ON t.id = a.type_id
WHERE `+where+`) x), 'null'::jsonb)`, &s, key)
	if err != nil {
		return unavailable()
	}
	switch {
	case s == nil || s.Anonymised:
		return refused("invalid")
	case s.EventID != eventID:
		return refused("other_event")
	case s.Status != "active":
		return refusedTicket(s)
	case s.CheckedInAt != nil:
		return scanAnswer("already", s.checkedIn)
	}
	return refused("invalid")
}

// refusedTicket refuses a ticket of this event that is not active; the name is
// shown because it is this event's ticket.
func refusedTicket(s *ticketState) sdk.HTTPResponse {
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "result": "refused", "reason": s.Status, "attendee": s.checkedIn})
}

// serveUncheck answers POST /admin/attendees/{id}/uncheck: the ticket is not
// checked in any more. A ticket that was not checked in is a 409.
func serveUncheck(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	n, err := dbExecN(`UPDATE attendees SET checked_in_at = NULL, checked_in_by = NULL WHERE id = $1 AND checked_in_at IS NOT NULL`, id)
	if err != nil {
		return unavailable()
	}
	if n == 0 {
		return failReply(http.StatusConflict, checkinNotYet)
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true})
}
