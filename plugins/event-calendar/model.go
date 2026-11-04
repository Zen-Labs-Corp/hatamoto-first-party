package main

import (
	"encoding/json"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	listPageSize = 200
	maxListPages = 5
)

// now is the clock; tests replace it.
var now = time.Now

// Event is one published event entity with a usable start time.
type Event struct {
	ID, Slug, Name, URL, Locale string
	Description, Image          string
	Venue, Address, TicketURL   string
	Start, End                  time.Time
	AllDay, HasEnd              bool
	Updated                     time.Time
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
	var evs []Event
	err := eachEvent(s, func(e Event) bool {
		evs = append(evs, e)
		return false
	})
	return evs, err
}

// eachEvent pages the published events and passes each one to visit until it
// returns true, so a caller that has what it needs reads no further page.
func eachEvent(s settings, visit func(Event) bool) error {
	cursor := ""
	for page := 0; page < maxListPages; page++ {
		args := map[string]any{
			"schema_slug": s.SchemaSlug,
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
		if err := callHost(sdk.FnEntitiesList, args, &out); err != nil {
			if page == 0 {
				return err
			}
			return nil
		}
		for _, raw := range out.Items {
			if e, ok := eventFromEntity(raw, s); ok && visit(e) {
				return nil
			}
		}
		if out.NextCursor == "" {
			return nil
		}
		cursor = out.NextCursor
	}
	return nil
}
