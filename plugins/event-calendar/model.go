package main

import (
	"encoding/json"
	"sort"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	listPageSize = 200
	maxListPages = 5
	maxRefIDs    = 20
)

// now is the clock; tests replace it.
var now = time.Now

// clock is the elapsed-time source of the call budget; tests replace it.
var clock = time.Now

// Event is one published event entity with a usable start time.
type Event struct {
	ID, Slug, Name, URL, Locale string
	Description, Image          string
	Venue, Address, TicketURL   string
	Start, End                  time.Time
	AllDay, HasEnd              bool
	Updated                     time.Time

	SchemaSlug, TimeZone             string
	EventStatus, AttendanceMode      string
	OnlineURL, Website               string
	VenueID, SeriesID, VenueCity     string
	OrganizerIDs, CategoryIDs        []string
	Price, Currency                  string
	PriceAmount                      *float64
	Featured                         bool
	SourceKind, SourceUID, SourceURL string
}

// entityDTO is the part of the host's entity JSON this plugin reads.
type entityDTO struct {
	ID         string          `json:"id"`
	SchemaSlug string          `json:"schema_slug"`
	Slug       string          `json:"slug"`
	Locale     string          `json:"locale"`
	Name       string          `json:"name"`
	Status     string          `json:"status"`
	URL        string          `json:"url"`
	Attributes json.RawMessage `json:"attributes"`
	UpdatedAt  string          `json:"updated_at"`
}

// parseTime reads the stored time layouts: a date, a local date and time
// (with or without seconds) in loc, or RFC 3339 with an offset.
func parseTime(s string, loc *time.Location) (time.Time, bool) {
	s = strings.TrimSpace(s)
	if t, err := time.Parse(time.RFC3339, s); err == nil {
		return t, true
	}
	for _, layout := range []string{"2006-01-02T15:04:05", "2006-01-02T15:04", "2006-01-02"} {
		if t, err := time.ParseInLocation(layout, s, loc); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// attrString returns a string attribute, empty when missing or not a string.
func attrString(m map[string]any, key string) string {
	s, _ := m[key].(string)
	return strings.TrimSpace(s)
}

// attrBool reads a boolean attribute, also accepting the string "true".
func attrBool(m map[string]any, key string) bool {
	switch v := m[key].(type) {
	case bool:
		return v
	case string:
		return strings.EqualFold(strings.TrimSpace(v), "true")
	}
	return false
}

// attrIDs reads an array of strings (a reference field holds entity ids):
// trimmed, empty entries skipped, at most maxRefIDs.
func attrIDs(m map[string]any, key string) []string {
	list, _ := m[key].([]any)
	var out []string
	for _, v := range list {
		if s, _ := v.(string); strings.TrimSpace(s) != "" {
			out = append(out, strings.TrimSpace(s))
			if len(out) == maxRefIDs {
				break
			}
		}
	}
	return out
}

// attrFloat reads a number attribute, also accepting a numeric string; nil
// when missing or not a number.
func attrFloat(m map[string]any, key string) *float64 {
	switch v := m[key].(type) {
	case float64:
		return &v
	case string:
		if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil {
			return &f
		}
	}
	return nil
}

// startOfDay is midnight of t's day in loc.
func startOfDay(t time.Time, loc *time.Location) time.Time {
	y, m, d := t.In(loc).Date()
	return time.Date(y, m, d, 0, 0, 0, 0, loc)
}

// eventFromEntity maps a published entity to an Event; false when it is not
// published or has no parseable start.
func eventFromEntity(raw entityDTO, s settings) (Event, bool) {
	if raw.Status != "published" {
		return Event{}, false
	}
	var attrs map[string]any
	if len(raw.Attributes) == 0 || json.Unmarshal(raw.Attributes, &attrs) != nil {
		return Event{}, false
	}
	loc := s.location()
	tz := attrString(attrs, "time_zone")
	if tz != "" {
		if l, err := time.LoadLocation(tz); err == nil {
			loc = l
		}
	}
	start, ok := parseTime(attrString(attrs, "start_at"), loc)
	if !ok {
		return Event{}, false
	}
	e := Event{
		ID: raw.ID, Slug: raw.Slug, Name: raw.Name, URL: raw.URL, Locale: raw.Locale,
		Description: attrString(attrs, "description"),
		Image:       attrString(attrs, "image"),
		Venue:       attrString(attrs, "venue_name"),
		Address:     attrString(attrs, "venue_address"),
		TicketURL:   attrString(attrs, "ticket_url"),
		AllDay:      attrBool(attrs, "all_day"),
		Start:       start,

		SchemaSlug:     raw.SchemaSlug,
		TimeZone:       tz,
		EventStatus:    attrString(attrs, "event_status"),
		AttendanceMode: attrString(attrs, "attendance_mode"),
		OnlineURL:      attrString(attrs, "online_url"),
		Website:        attrString(attrs, "website"),
		OrganizerIDs:   attrIDs(attrs, "organizers"),
		CategoryIDs:    attrIDs(attrs, "category"),
		VenueCity:      attrString(attrs, "venue_city"),
		Price:          attrString(attrs, "price"),
		PriceAmount:    attrFloat(attrs, "price_amount"),
		Currency:       attrString(attrs, "currency"),
		Featured:       attrBool(attrs, "featured"),
		SourceKind:     attrString(attrs, "source_kind"),
		SourceUID:      attrString(attrs, "source_uid"),
		SourceURL:      attrString(attrs, "source_url"),
	}
	if ids := attrIDs(attrs, "venue"); len(ids) > 0 {
		e.VenueID = ids[0]
	}
	if ids := attrIDs(attrs, "series"); len(ids) > 0 {
		e.SeriesID = ids[0]
	}
	if e.EventStatus == "" {
		e.EventStatus = "scheduled"
	}
	if e.AttendanceMode == "" {
		e.AttendanceMode = "offline"
	}
	if e.Image == "" {
		e.Image = attrString(attrs, "featured_image")
	}
	if end, ok := parseTime(attrString(attrs, "end_at"), loc); ok && !end.Before(start) {
		e.End, e.HasEnd = end, true
	}
	if e.AllDay {
		e.Start = startOfDay(e.Start, loc)
		if e.HasEnd {
			e.End = startOfDay(e.End, loc)
		}
	}
	if t, err := time.Parse(time.RFC3339, raw.UpdatedAt); err == nil {
		e.Updated = t
	}
	return e, true
}

// endsAt is the moment the event is over: its end, else its start; for an
// all-day event the end of its last day in loc.
func (e Event) endsAt(loc *time.Location) time.Time {
	last := e.Start
	if e.HasEnd {
		last = e.End
	}
	if !e.AllDay {
		return last
	}
	y, m, d := last.In(loc).Date()
	return time.Date(y, m, d+1, 0, 0, 0, 0, loc)
}

// split divides events at the given moment: upcoming (not over yet) by start
// ascending, past by start descending; ties by name then id.
func split(evs []Event, at time.Time, loc *time.Location) (upcoming, past []Event) {
	for _, e := range evs {
		if at.Before(e.endsAt(loc)) {
			upcoming = append(upcoming, e)
		} else {
			past = append(past, e)
		}
	}
	less := func(a, b Event) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		return strings.Compare(a.ID, b.ID)
	}
	sort.SliceStable(upcoming, func(i, j int) bool {
		a, b := upcoming[i], upcoming[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.Before(b.Start)
		}
		return less(a, b) < 0
	})
	sort.SliceStable(past, func(i, j int) bool {
		a, b := past[i], past[j]
		if !a.Start.Equal(b.Start) {
			return a.Start.After(b.Start)
		}
		return less(a, b) < 0
	})
	return upcoming, past
}

// loadEvents reads the published entities of the configured type (at most
// maxListPages pages). An error on the first page is returned; a later page
// failing keeps what was read.
func loadEvents(s settings) ([]Event, error) {
	return loadEventsUntil(s, time.Time{})
}

// loadEventsUntil is loadEvents that starts no further page once until has
// passed (zero: no limit) and keeps what was read.
func loadEventsUntil(s settings, until time.Time) ([]Event, error) {
	var evs []Event
	err := eachEvent(s, until, func(e Event) bool {
		evs = append(evs, e)
		return false
	})
	return evs, err
}

// eachEvent pages the published events of every accepted content type and
// passes each one to visit until it returns true, so a caller that has what it
// needs reads no further page. At most maxListPages pages are read in all, and
// none after the first once until (if set) has passed. An error on the very
// first page is returned; a later failure keeps what was read. An event
// listed twice is visited once.
func eachEvent(s settings, until time.Time, visit func(Event) bool) error {
	seen := map[string]bool{}
	pages := 0
	for si, slug := range s.slugs() {
		cursor := ""
		for pages < maxListPages {
			if pages > 0 && !until.IsZero() && !clock().Before(until) {
				return nil
			}
			args := map[string]any{
				"schema_slug": slug,
				"status":      "published",
				"limit":       listPageSize,
			}
			if cursor != "" {
				args["cursor"] = cursor
			}
			var out struct {
				Items      []entityDTO `json:"items"`
				NextCursor string      `json:"next_cursor"`
			}
			err := callHost(sdk.FnEntitiesList, args, &out)
			pages++
			if err != nil {
				if si == 0 && pages == 1 {
					return err
				}
				break
			}
			for _, raw := range out.Items {
				if raw.SchemaSlug != "" && !s.accepts(raw.SchemaSlug) || seen[raw.ID] {
					continue
				}
				seen[raw.ID] = true
				if e, ok := eventFromEntity(raw, s); ok && visit(e) {
					return nil
				}
			}
			if out.NextCursor == "" {
				break
			}
			cursor = out.NextCursor
		}
	}
	return nil
}
