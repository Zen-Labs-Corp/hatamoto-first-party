package main

import (
	"encoding/json"
	"net/http"
	"strconv"

	"hatamoto.dev/sdk"
)

// attendeesPerPage is the page size of the attendee list.
const attendeesPerPage = 100

// maxResends and resendGap are the resend guard: at most 5 per order, one per
// 10 minutes (kept in the statement that records the resend).
const (
	maxResends = 5
	resendGap  = "10 minutes"
)

// parseID reads the id of a path: digits only, above zero.
func parseID(raw string) (int64, bool) {
	if !idDigitsRe.MatchString(raw) {
		return 0, false
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	return id, err == nil && id > 0
}

// serveAttendees answers GET /admin/events/{id}/attendees?page=: tickets with
// their buyer, and the waitlist, 100 of each per page. Anonymised buyers show
// as "(removed)".
func serveAttendees(u *sdk.HTTPUser, eventID string, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	page, err := strconv.Atoi(queryValue(query["page"]))
	if err != nil || page < 1 || page > 100000 {
		page = 1
	}
	offset := (page - 1) * attendeesPerPage
	var attendees, waiting []json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id), '[]'::jsonb) FROM (
SELECT a.id, a.order_id, CASE WHEN o.anonymised_at IS NULL THEN o.name ELSE '(removed)' END AS name, o.email,
  t.name AS type_name, a.status, a.created_at, o.mailed_at, o.answers
FROM attendees a JOIN orders o ON o.id = a.order_id JOIN ticket_types t ON t.id = a.type_id
WHERE a.event_id = $1 ORDER BY a.id LIMIT $2 OFFSET $3) x`, &attendees, eventID, attendeesPerPage+1, offset)
	if err != nil {
		return unavailable()
	}
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(x) ORDER BY x.id), '[]'::jsonb) FROM (
SELECT w.id, w.name, w.email, t.name AS type_name, w.status, w.created_at
FROM waitlist w JOIN ticket_types t ON t.id = w.type_id
WHERE w.event_id = $1 ORDER BY w.id LIMIT $2 OFFSET $3) x`, &waiting, eventID, attendeesPerPage+1, offset)
	if err != nil {
		return unavailable()
	}
	more := len(attendees) > attendeesPerPage || len(waiting) > attendeesPerPage
	attendees = attendees[:min(len(attendees), attendeesPerPage)]
	waiting = waiting[:min(len(waiting), attendeesPerPage)]
	return jsonReply(http.StatusOK, map[string]any{
		"attendees": attendees, "waitlist": waiting, "page": page, "has_more": more,
	})
}

// serveResend answers POST /admin/attendees/{id}/resend: the confirmation mail
// of the attendee's order again. One statement checks and records the resend,
// so two clicks cannot both pass the guard.
func serveResend(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var orderID *int64
	err := dbJSON(`WITH u AS (UPDATE orders SET resent_at = now(), resend_count = resend_count + 1
    WHERE id = (SELECT order_id FROM attendees WHERE id = $1) AND anonymised_at IS NULL
      AND resend_count < $2 AND (resent_at IS NULL OR resent_at < now() - interval '`+resendGap+`') RETURNING id)
SELECT coalesce((SELECT to_jsonb(id) FROM u), 'null'::jsonb)`, &orderID, id, maxResends)
	if err != nil {
		return unavailable()
	}
	if orderID == nil {
		return failReply(http.StatusTooManyRequests, "resent recently; try again in a few minutes")
	}
	queued := false
	if _, err := deliver(kindConfirm, *orderID); err != nil {
		queued = enqueue(kindConfirm, *orderID) == nil
		if !queued {
			return unavailable()
		}
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "queued": queued})
}

// serveCancel answers POST /admin/attendees/{id}/cancel: frees the seat and
// offers it to the waitlist. The attendee gets no email.
func serveCancel(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	id, ok := parseID(rawID)
	if !ok {
		return failReply(http.StatusBadRequest, "bad id")
	}
	_, promoted, ok := cancelAttendee(id)
	if !ok {
		return failReply(http.StatusConflict, "not an active ticket")
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "promoted": promoted})
}
