package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// The admin routes of the Events tab. They are not public: core lets the
// owner and editor roles through, and every handler still refuses a request
// without a user.

const (
	listPageSize = 50
	maxSearch    = 100
	maxCursor    = 512
)

var idDigitsRe = regexp.MustCompile(`^[0-9]{1,18}$`)

// eventRow is one line of the events list.
type eventRow struct {
	ID       string `json:"id"`
	Name     string `json:"name"`
	URL      string `json:"url"`
	StartsAt string `json:"starts_at"`
	Types    int    `json:"types"`
	Capacity int    `json:"capacity"`
	Taken    int    `json:"taken"`
	Waiting  int    `json:"waiting"`
}

// eventStats are the ticket numbers of one event.
type eventStats struct {
	EventID  string `json:"event_id"`
	Types    int    `json:"types"`
	Capacity int    `json:"capacity"`
	Taken    int    `json:"taken"`
	Waiting  int    `json:"waiting"`
}

// listPage is one page of the events of the accepted content types. The
// cursor is "<index of the type>:<host cursor>", so one cursor walks every
// type in turn.
func listPage(s settings, cursor, search string) (items []entityDTO, next string, err error) {
	slugs := s.slugs()
	idx, hostCursor := 0, ""
	if cursor != "" {
		i, rest, ok := strings.Cut(cursor, ":")
		n, convErr := strconv.Atoi(i)
		if !ok || convErr != nil || n < 0 || n >= len(slugs) {
			return nil, "", errors.New("bad cursor")
		}
		idx, hostCursor = n, rest
	}
	for ; idx < len(slugs); idx, hostCursor = idx+1, "" {
		args := map[string]any{"schema_slug": slugs[idx], "status": "published", "limit": listPageSize}
		if hostCursor != "" {
			args["cursor"] = hostCursor
		}
		if search != "" {
			args["search"] = search
		}
		var out struct {
			Items      []entityDTO `json:"items"`
			NextCursor string      `json:"next_cursor"`
		}
		if err := callHost(sdk.FnEntitiesList, args, &out); err != nil {
			var he *sdk.HostError
			if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
				continue // the type does not exist: it has no events
			}
			return nil, "", err
		}
		switch {
		case out.NextCursor != "":
			next = strconv.Itoa(idx) + ":" + out.NextCursor
		case idx+1 < len(slugs):
			next = strconv.Itoa(idx+1) + ":"
		}
		return out.Items, next, nil
	}
	return nil, "", nil
}

// serveEvents answers GET /admin/events?cursor=&search=: the events with
// their ticket numbers. The numbers come from one query over the page's ids;
// no event is read one by one.
func serveEvents(u *sdk.HTTPUser, query map[string][]string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	cursor, search := queryValue(query["cursor"]), queryValue(query["search"])
	if len(cursor) > maxCursor || utf8.RuneCountInString(search) > maxSearch {
		return failReply(http.StatusBadRequest, "bad request")
	}
	items, next, err := listPage(loadSettings(), cursor, search)
	if err != nil {
		if err.Error() == "bad cursor" {
			return failReply(http.StatusBadRequest, "bad request")
		}
		return unavailable()
	}
	ids := make([]string, 0, len(items))
	for _, it := range items {
		ids = append(ids, it.ID)
	}
	stats := map[string]eventStats{}
	if len(ids) > 0 {
		doc, _ := json.Marshal(ids)
		var rows []eventStats
		err := dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (
SELECT tt.event_id, count(*)::int AS types, sum(tt.capacity)::int AS capacity, sum(tt.taken)::int AS taken,
    (SELECT count(*)::int FROM waitlist w WHERE w.event_id = tt.event_id AND w.status = 'waiting') AS waiting
FROM ticket_types tt
WHERE tt.event_id IN (SELECT jsonb_array_elements_text($1::jsonb))
GROUP BY tt.event_id) t`, &rows, string(doc))
		if err != nil {
			return unavailable()
		}
		for _, r := range rows {
			stats[r.EventID] = r
		}
	}
	events := make([]eventRow, 0, len(items))
	for _, it := range items {
		var attrs map[string]any
		_ = json.Unmarshal(it.Attributes, &attrs)
		st := stats[it.ID]
		events = append(events, eventRow{
			ID: it.ID, Name: it.Name, URL: it.URL, StartsAt: attrString(attrs, "start_at"),
			Types: st.Types, Capacity: st.Capacity, Taken: st.Taken, Waiting: st.Waiting,
		})
	}
	return jsonReply(http.StatusOK, map[string]any{"events": events, "next_cursor": next})
}

// serveEventDetail answers GET /admin/events/{id}: the event, its ticket
// types and fields. Reading it also records its dates, so the retention job
// knows when it ended as soon as tickets exist.
func serveEventDetail(u *sdk.HTTPUser, id string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	if !idOK(id) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	s := loadSettings()
	e, ok, err := readEvent(id, s)
	if err != nil {
		return unavailable()
	}
	if !ok {
		return failReply(http.StatusNotFound, "not found")
	}
	_ = snapshotEvent(e)
	types, err := listTypes(id)
	if err != nil {
		return unavailable()
	}
	fields, err := listFields(id)
	if err != nil {
		return unavailable()
	}
	ev := map[string]any{"id": e.ID, "name": e.Name, "url": e.URL, "all_day": e.AllDay}
	if !e.Start.IsZero() {
		ev["starts_at"] = e.Start.UTC().Format("2006-01-02T15:04:05Z")
	}
	if e.HasEnd {
		ev["ends_at"] = e.End.UTC().Format("2006-01-02T15:04:05Z")
	}
	return jsonReply(http.StatusOK, map[string]any{
		"event": ev, "types": types, "fields": fields, "default_capacity": s.DefaultCapacity,
	})
}

// typeBody is the body of POST /admin/events/{id}/types. A missing waitlist
// or on_sale means true; a missing capacity means the default capacity; a
// missing price means free.
type typeBody struct {
	ID       int64  `json:"id"`
	Name     string `json:"name"`
	Capacity *int   `json:"capacity"`
	Waitlist *bool  `json:"waitlist"`
	OnSale   *bool  `json:"on_sale"`
	Position int    `json:"position"`
	Price    *int64 `json:"price_cents"`
}

// serveSaveType answers POST /admin/events/{id}/types: add a ticket type, or
// change the one named by id. It answers the event's types.
func serveSaveType(r sdk.HTTPRequest, eventID string) sdk.HTTPResponse {
	if r.User == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b typeBody
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	s := loadSettings()
	t := ticketType{ID: b.ID, EventID: eventID, Waitlist: true, OnSale: true, Position: b.Position, Capacity: s.DefaultCapacity}
	name, ok := cleanText(b.Name, maxNameRunes)
	if !ok || b.ID < 0 || b.Position < 0 || b.Position > maxPosition {
		return failReply(http.StatusBadRequest, "bad request")
	}
	t.Name = name
	if b.Capacity != nil {
		t.Capacity = *b.Capacity
	}
	if t.Capacity < 1 || t.Capacity > maxCapacity {
		return failReply(http.StatusBadRequest, "capacity must be between 1 and 100000")
	}
	if b.Price != nil {
		t.Price = *b.Price
	}
	if t.Price < 0 || t.Price > maxPriceCents {
		return failReply(http.StatusBadRequest, "price must be between 0 and 99999999")
	}
	if b.Waitlist != nil {
		t.Waitlist = *b.Waitlist
	}
	if b.OnSale != nil {
		t.OnSale = *b.OnSale
	}
	e, found, err := readEvent(eventID, s)
	if err != nil {
		return unavailable()
	}
	if !found {
		return failReply(http.StatusNotFound, "not found")
	}
	switch err := saveType(t); {
	case errors.Is(err, errCapacityBelowTaken), errors.Is(err, errTooManyTypes):
		return failReply(http.StatusConflict, err.Error())
	case errors.Is(err, errNotFound):
		return failReply(http.StatusNotFound, "not found")
	case err != nil:
		return unavailable()
	}
	_ = snapshotEvent(e)
	types, err := listTypes(eventID)
	if err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "types": types})
}

// serveDeleteType answers POST /admin/types/{id}/delete.
func serveDeleteType(u *sdk.HTTPUser, rawID string) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	if !idDigitsRe.MatchString(rawID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	id, _ := strconv.ParseInt(rawID, 10, 64)
	closed, err := deleteType(id)
	switch {
	case errors.Is(err, errNotFound):
		return failReply(http.StatusNotFound, "not found")
	case err != nil:
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "closed": closed})
}

// serveSaveFields answers POST /admin/events/{id}/fields with the event's
// whole field list.
func serveSaveFields(r sdk.HTTPRequest, eventID string) sdk.HTTPResponse {
	if r.User == nil {
		return forbidden()
	}
	if !idOK(eventID) {
		return failReply(http.StatusBadRequest, "bad id")
	}
	var b struct {
		Fields []field `json:"fields"`
	}
	if msg := decodeBody(r, &b); msg != "" {
		return failReply(http.StatusBadRequest, msg)
	}
	fields, err := validateFields(b.Fields)
	if err != nil {
		return failReply(http.StatusBadRequest, err.Error())
	}
	if _, found, err := readEvent(eventID, loadSettings()); err != nil {
		return unavailable()
	} else if !found {
		return failReply(http.StatusNotFound, "not found")
	}
	if err := saveFields(eventID, fields); err != nil {
		return unavailable()
	}
	return jsonReply(http.StatusOK, map[string]any{"ok": true, "fields": fields})
}

// serveSettings answers GET /admin/settings: the settings the page shows.
// The confirmation text is only said to be set; there is no secret to show.
func serveSettings(u *sdk.HTTPUser) sdk.HTTPResponse {
	if u == nil {
		return forbidden()
	}
	s := loadSettings()
	feePer, feePayer := "order", "buyer"
	if s.FeePerTicket {
		feePer = "ticket"
	}
	if s.FeeAbsorbed {
		feePayer = "organizer"
	}
	return jsonReply(http.StatusOK, map[string]any{
		"currency": s.Currency, "sender_name": s.SenderName, "confirmation_text_set": s.ConfirmationText != "",
		"default_capacity": s.DefaultCapacity, "retention_months": s.RetentionMonths,
		"site_url": s.SiteURL, "schema_slug": s.SchemaSlug,
		"stripe_configured": s.stripeReady(), "paypal_configured": s.paypalReady(),
		"paypal_webhook_set": s.PayPalWebhookID != "", "paypal_sandbox": s.PayPalSandbox,
		"fee_fixed_cents": s.FeeFixed, "fee_percent_bp": s.FeeBP, "fee_per": feePer, "fee_payer": feePayer,
		"currency_exponent": currencyExp(s.Currency), "can_refund": u.Role == "owner",
	})
}
