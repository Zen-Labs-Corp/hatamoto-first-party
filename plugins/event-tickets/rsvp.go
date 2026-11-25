package main

import (
	"encoding/json"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"slices"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// The public RSVP routes. Every answer is generic; host and database error
// text never reaches the visitor.

const (
	maxTicketsPerOrder = 10
	maxOrdersPerEmail  = 3
	maxOrdersPerHour   = 200
	maxWaitingPerEvent = 500
	maxEmailLen        = 254
	maxPersonName      = 120
	maxAnswerRunes     = 200
	fieldsError        = "check the highlighted fields"
	laterError         = "try again later"
	tooManyError       = "too many requests, try again later"
	maxRSVPBody        = maxBodyBytes
)

type rsvpItem struct {
	Type int64 `json:"type"`
	Qty  int   `json:"qty"`
}

// rsvpBody is the JSON the island posts. Website is the honeypot: people
// leave it empty, scripts that fill every field do not.
type rsvpBody struct {
	Event        string            `json:"event"`
	Items        []rsvpItem        `json:"items"`
	WaitlistType int64             `json:"waitlist_type"`
	Name         string            `json:"name"`
	Email        string            `json:"email"`
	Answers      map[string]string `json:"answers"`
	Token        string            `json:"token"`
	Website      string            `json:"website"`
}

// tokenType is a ticket type as the form sees it, with live counts.
type tokenType struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Left     int    `json:"left"`
	SoldOut  bool   `json:"sold_out"`
	Waitlist bool   `json:"waitlist"`
}

func rsvpOK(extra map[string]any) sdk.HTTPResponse {
	body := map[string]any{"ok": true}
	for k, v := range extra {
		body[k] = v
	}
	return jsonReply(http.StatusOK, body)
}

// serveToken issues the time-trap token with what the form needs: the types
// on sale, with live counts, and the custom fields of the event.
func serveToken(query map[string][]string) sdk.HTTPResponse {
	eventID := queryValue(query["event"])
	if !idOK(eventID) {
		return notFound()
	}
	types, err := listTypes(eventID)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	out := []tokenType{}
	for _, t := range types {
		if t.OnSale {
			left := max(t.Capacity-t.Taken, 0)
			out = append(out, tokenType{ID: t.ID, Name: t.Name, Left: left, SoldOut: left == 0, Waitlist: t.Waitlist})
		}
	}
	if len(out) == 0 {
		return notFound()
	}
	fields, err := listFields(eventID)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	key, err := hmacSecret()
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	issued := strconv.FormatInt(now().UnixMilli(), 10)
	return jsonReply(http.StatusOK, map[string]any{
		"token":  issued + "." + tokenMAC(key, issued),
		"types":  out,
		"fields": fields,
	})
}

// serveRSVP takes one RSVP. Bots get the answer people get ("ok") and nothing
// is stored; only a malformed or invalid post is told so.
func serveRSVP(r sdk.HTTPRequest) sdk.HTTPResponse {
	if mt, _, err := mime.ParseMediaType(r.Headers["content-type"]); err != nil || mt != "application/json" {
		return failReply(http.StatusUnsupportedMediaType, fieldsError)
	}
	switch {
	case r.BodyB64 != "":
		return failReply(http.StatusBadRequest, fieldsError)
	case len(r.Body) > maxRSVPBody:
		return failReply(http.StatusRequestEntityTooLarge, fieldsError)
	}
	var b rsvpBody
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		return failReply(http.StatusBadRequest, fieldsError)
	}
	if strings.TrimSpace(b.Website) != "" {
		return rsvpOK(nil)
	}
	key, err := hmacSecret()
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if !tokenFresh(key, b.Token) {
		return rsvpOK(nil)
	}
	name := capRunes(plainText(b.Name, false), maxPersonName+1)
	email := strings.TrimSpace(b.Email)
	waitlisting := b.WaitlistType != 0
	switch {
	case !idOK(b.Event), name == "", utf8.RuneCountInString(name) > maxPersonName,
		len(email) > maxEmailLen, !plainAddress(email),
		waitlisting == (len(b.Items) > 0):
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	items, ok := cleanItems(b.Items)
	if !waitlisting && !ok {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}

	s := loadSettings()
	e, found, err := readEvent(b.Event, s)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if !found || !e.open(now()) {
		return jsonReply(http.StatusNotFound, map[string]any{"ok": false, "closed": true, "error": "closed"})
	}
	types, err := listTypes(b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	counts, err := countEvent(b.Event, email)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if counts.Hour >= maxOrdersPerHour {
		return failReply(http.StatusTooManyRequests, tooManyError)
	}
	if waitlisting {
		return rsvpWaitlist(key, b, name, email, types, counts)
	}
	if counts.ByEmail >= maxOrdersPerEmail {
		return failReply(http.StatusTooManyRequests, tooManyError)
	}
	for _, it := range items {
		if !slices.ContainsFunc(types, func(t ticketType) bool { return t.ID == it.Type && t.OnSale }) {
			return failReply(http.StatusUnprocessableEntity, fieldsError)
		}
	}
	fields, err := listFields(b.Event)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	answers, ok := cleanAnswers(fields, b.Answers)
	if !ok {
		return failReply(http.StatusUnprocessableEntity, fieldsError)
	}
	return rsvpReserve(key, b.Event, orderIn{Name: name, Email: email, Answers: answers}, items)
}

// cleanItems checks the requested seats: every quantity at least one, types
// not repeated, at most maxTicketsPerOrder seats in all.
func cleanItems(in []rsvpItem) ([]rsvpItem, bool) {
	total := 0
	seen := map[int64]bool{}
	for _, it := range in {
		if it.Qty < 1 || it.Qty > maxTicketsPerOrder || it.Type < 1 || seen[it.Type] {
			return nil, false
		}
		seen[it.Type] = true
		total += it.Qty
	}
	if total < 1 || total > maxTicketsPerOrder {
		return nil, false
	}
	return in, true
}

// cleanAnswers checks the answers against the event's fields: no unknown key,
// every required one given, a choice among its options, a checkbox "yes" or
// empty. Empty answers are not kept.
func cleanAnswers(fields []field, in map[string]string) (map[string]string, bool) {
	out := map[string]string{}
	for k := range in {
		if !slices.ContainsFunc(fields, func(f field) bool { return f.Key == k }) {
			return nil, false
		}
	}
	for _, f := range fields {
		v := plainText(in[f.Key], false)
		switch {
		case utf8.RuneCountInString(v) > maxAnswerRunes:
			return nil, false
		case v == "" && f.Required:
			return nil, false
		case v == "":
			continue
		case f.Kind == "select" && !slices.Contains(f.Options, v):
			return nil, false
		case f.Kind == "checkbox" && v != "yes":
			return nil, false
		}
		out[f.Key] = v
	}
	return out, true
}

// rsvpReserve takes the seats type by type. When a later type is sold out the
// seats already taken are given back and nothing stays stored.
func rsvpReserve(key []byte, eventID string, o orderIn, items []rsvpItem) sdk.HTTPResponse {
	var orderID int64
	var tickets []string
	for i, it := range items {
		codes := make([]string, it.Qty)
		for j := range codes {
			c, err := newCode()
			if err != nil {
				return rsvpAbort(orderID, http.StatusServiceUnavailable, nil)
			}
			codes[j] = c
		}
		var ok bool
		var err error
		if i == 0 {
			orderID, ok, err = reserve(eventID, it.Type, o, codes)
		} else {
			ok, err = reserveMore(orderID, eventID, it.Type, codes)
		}
		if err != nil {
			return rsvpAbort(orderID, http.StatusServiceUnavailable, nil)
		}
		if !ok {
			return rsvpAbort(orderID, http.StatusConflict, []int64{it.Type})
		}
		for _, c := range codes {
			tickets = append(tickets, ticketPath(signCode(key, kindTicket, c)))
		}
	}
	sendNow(kindConfirm, orderID)
	return rsvpOK(map[string]any{"tickets": tickets})
}

// rsvpAbort gives back what an unfinished order took and answers.
func rsvpAbort(orderID int64, status int, soldOutTypes []int64) sdk.HTTPResponse {
	if orderID != 0 && releaseOrder(orderID) != nil {
		status = http.StatusServiceUnavailable
	}
	if status == http.StatusConflict {
		return jsonReply(status, map[string]any{"ok": false, "error": "sold out", "sold_out": soldOutTypes})
	}
	return failReply(status, laterError)
}

// rsvpWaitlist adds the person to the waitlist of a sold-out type.
func rsvpWaitlist(key []byte, b rsvpBody, name, email string, types []ticketType, counts eventCounts) sdk.HTTPResponse {
	i := slices.IndexFunc(types, func(t ticketType) bool { return t.ID == b.WaitlistType })
	if i < 0 || !types[i].OnSale || !types[i].Waitlist || types[i].Taken < types[i].Capacity {
		return failReply(http.StatusConflict, "no waitlist for this ticket")
	}
	if counts.Waiting >= maxWaitingPerEvent {
		return failReply(http.StatusTooManyRequests, tooManyError)
	}
	code, err := newCode()
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	id, err := joinWaitlist(b.Event, b.WaitlistType, name, email, code)
	if err != nil {
		return failReply(http.StatusServiceUnavailable, laterError)
	}
	if id != nil {
		sendNow(kindWaitlistJoined, *id)
	}
	return rsvpOK(map[string]any{"waitlisted": true})
}

// plainAddress reports whether s is one plain address, as mail.send needs:
// no display name, no control characters.
func plainAddress(s string) bool {
	if s == "" || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}
