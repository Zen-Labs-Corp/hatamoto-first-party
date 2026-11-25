package main

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/sdk"
)

// The public seat routes. Availability is the same for everybody and carries no
// buyer data, so it is the one answer here that may be shared for a few
// seconds; holding and releasing seats are answered no-store.

const (
	maxSeatBody   = 4 << 10
	seatMapMaxAge = "public, max-age=5"
)

// seatHoldBody is the JSON the picker posts to hold one seat: the section id,
// the row index (from 0) and the seat number (from 1) as served by /seats/{event}.
// Hold is the token of the buyer's earlier picks; it is empty for the first seat.
type seatHoldBody struct {
	Event   string `json:"event"`
	Section int64  `json:"section"`
	Row     int    `json:"row"`
	Num     int    `json:"num"`
	Hold    string `json:"hold"`
	Token   string `json:"token"`
	Website string `json:"website"`
}

// serveSeatMap answers GET /seats/{event}: the sections bound to the event's
// ticket types, grid rows as one state letter per seat.
func serveSeatMap(eventID string) sdk.HTTPResponse {
	if !idOK(eventID) {
		return failReply(http.StatusNotFound, "not found")
	}
	m, err := seatAvailability(eventID, loadSettings().Currency)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if m == nil {
		return failReply(http.StatusNotFound, "not found")
	}
	body, _ := json.Marshal(map[string]any{"ok": true, "sections": m.Sections})
	return sdk.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Cache-Control": seatMapMaxAge},
		Body:    string(body),
	}
}

// serveSeatHold answers POST /seats/hold: hold one seat for the buyer's hold
// code, a new one for the first seat. Bots get the answer people get.
func serveSeatHold(r sdk.HTTPRequest) sdk.HTTPResponse {
	var b seatHoldBody
	if bad := readPostJSON(r, maxSeatBody, &b); bad != nil {
		return *bad
	}
	if b.Website != "" {
		return rsvpOK(nil)
	}
	key, err := hmacSecret()
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if !tokenFresh(key, b.Token) {
		return rsvpOK(nil)
	}
	if !idOK(b.Event) || b.Section < 1 || b.Row < 0 || b.Row >= maxRows || b.Num < 1 || b.Num > maxSeatsPerRow {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	e, found, err := readEvent(b.Event, loadSettings())
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if !found || !e.open(now()) {
		return jsonReply(http.StatusNotFound, map[string]any{"ok": false, "closed": true, "error": "closed"})
	}
	code := ""
	if b.Hold != "" {
		var ok bool
		if code, ok = verifyToken(key, kindHold, b.Hold); !ok {
			return failReply(http.StatusUnprocessableEntity, fieldsError)
		}
	} else if code, err = newCode(); err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	res, err := holdSeat(b.Event, code, b.Section, b.Row, b.Num)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	switch res.Status {
	case "held":
		return rsvpOK(map[string]any{"hold": signCode(key, kindHold, code), "seat_id": res.SeatID, "label": res.Label, "expires_in": res.ExpiresIn})
	case "full":
		return failReply(http.StatusTooManyRequests, "full")
	}
	return failReply(http.StatusConflict, "taken") // taken, or no seat to sell any more
}

// serveSeatRelease answers DELETE /seats/hold/{token}?event=&seat=: let go of
// one seat of the hold, or of all of them without seat. The token is the key:
// it only names the buyer's own picks.
func serveSeatRelease(tok string, query map[string][]string) sdk.HTTPResponse {
	key, err := hmacSecret()
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	code, ok := verifyToken(key, kindHold, tok)
	if !ok {
		return notFound()
	}
	eventID := queryValue(query["event"])
	var seat int64
	if raw := queryValue(query["seat"]); raw != "" {
		if seat, ok = parseID(raw); !ok {
			return failReply(http.StatusUnprocessableEntity, fieldsError)
		}
	}
	if !idOK(eventID) {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	n, err := releaseSeatHold(eventID, code, seat)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	return rsvpOK(map[string]any{"released": n})
}
