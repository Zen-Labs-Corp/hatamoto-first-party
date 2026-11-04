package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"
)

func mustLoc(t *testing.T, name string) *time.Location {
	t.Helper()
	loc, err := time.LoadLocation(name)
	if err != nil {
		t.Fatal(err)
	}
	return loc
}

func TestTzdataEmbedded(t *testing.T) {
	if loc := mustLoc(t, "Europe/Berlin"); loc.String() != "Europe/Berlin" {
		t.Fatalf("loc = %v", loc)
	}
}

func TestParseTimeLayouts(t *testing.T) {
	berlin := mustLoc(t, "Europe/Berlin")
	cases := []struct {
		in   string
		want string // UTC, RFC 3339
	}{
		{"2026-02-14", "2026-02-13T23:00:00Z"},
		{"2026-02-14T19:00", "2026-02-14T18:00:00Z"},
		{"2026-02-14T19:00:30", "2026-02-14T18:00:30Z"},
		{"2026-02-14T19:00:00Z", "2026-02-14T19:00:00Z"},
		{"2026-02-14T19:00:00+05:00", "2026-02-14T14:00:00Z"},
		{" 2026-02-14T19:00 ", "2026-02-14T18:00:00Z"},
	}
	for _, c := range cases {
		got, ok := parseTime(c.in, berlin)
		if !ok || got.UTC().Format(time.RFC3339) != c.want {
			t.Errorf("parseTime(%q) = %v, %v; want %s", c.in, got, ok, c.want)
		}
	}
	for _, bad := range []string{"", "tomorrow", "14/02/2026", "2026-13-01"} {
		if _, ok := parseTime(bad, berlin); ok {
			t.Errorf("parseTime(%q) accepted", bad)
		}
	}
}

func TestParseTimeDSTGapAndOverlap(t *testing.T) {
	london := mustLoc(t, "Europe/London")
	gap, ok := parseTime("2026-03-29T01:30", london) // does not exist
	if !ok {
		t.Fatal("gap time rejected")
	}
	if d := gap.UTC().Sub(time.Date(2026, 3, 29, 1, 30, 0, 0, time.UTC)); d < -time.Hour || d > time.Hour {
		t.Fatalf("gap time = %v", gap.UTC())
	}
	over, ok := parseTime("2026-10-25T01:30", london) // happens twice
	if !ok {
		t.Fatal("overlap time rejected")
	}
	u := over.UTC()
	if !u.Equal(time.Date(2026, 10, 25, 0, 30, 0, 0, time.UTC)) && !u.Equal(time.Date(2026, 10, 25, 1, 30, 0, 0, time.UTC)) {
		t.Fatalf("overlap time = %v", u)
	}
}

func entity(id, name, status, attrs string) entityDTO {
	return entityDTO{ID: id, SchemaSlug: "event", Slug: id, Name: name, Status: status,
		Attributes: json.RawMessage(attrs), UpdatedAt: "2026-01-02T03:04:05Z"}
}

func TestEventFromEntity(t *testing.T) {
	s := settings{SchemaSlug: "event", TimeZone: "UTC", DateFormat: "short"}
	e, ok := eventFromEntity(entity("a", "Jazz", "published",
		`{"start_at":"2026-02-14T19:00","end_at":"2026-02-14T21:30","venue_name":"Blue Room","venue_address":"12 High St","ticket_url":"https://t.example/x","description":"d","featured_image":"/i.png"}`), s)
	if !ok || e.Venue != "Blue Room" || e.Address != "12 High St" || e.TicketURL != "https://t.example/x" ||
		e.Description != "d" || e.Image != "/i.png" || !e.HasEnd || e.End.Sub(e.Start) != 150*time.Minute ||
		e.Updated.IsZero() {
		t.Fatalf("event = %+v, %v", e, ok)
	}
	if _, ok := eventFromEntity(entity("b", "Draft", "draft", `{"start_at":"2026-02-14"}`), s); ok {
		t.Error("draft accepted")
	}
	for _, attrs := range []string{`{}`, `{"start_at":"soon"}`, `not json`, ``} {
		if _, ok := eventFromEntity(entity("c", "X", "published", attrs), s); ok {
			t.Errorf("attrs %q accepted", attrs)
		}
	}
	e, _ = eventFromEntity(entity("d", "End first", "published", `{"start_at":"2026-02-14T19:00","end_at":"2026-02-14T18:00"}`), s)
	if e.HasEnd {
		t.Error("end before start kept")
	}
	e, _ = eventFromEntity(entity("e", "Market", "published", `{"start_at":"2026-03-07T10:00","end_at":"2026-03-08","all_day":"true"}`), s)
	if !e.AllDay || e.Start.Hour() != 0 || !e.HasEnd {
		t.Errorf("all-day = %+v", e)
	}
}

func TestEndsAtAndSplitBoundaries(t *testing.T) {
	loc := mustLoc(t, "Europe/Berlin")
	s := settings{SchemaSlug: "event", TimeZone: "Europe/Berlin"}
	mk := func(id, attrs string) Event {
		e, ok := eventFromEntity(entity(id, id, "published", attrs), s)
		if !ok {
			t.Fatalf("event %s rejected", id)
		}
		return e
	}
	timed := mk("timed", `{"start_at":"2026-02-14T19:00","end_at":"2026-02-14T21:30"}`)
	noEnd := mk("noend", `{"start_at":"2026-02-14T19:00"}`)
	allDay := mk("allday", `{"start_at":"2026-03-07","all_day":true}`)
	evs := []Event{timed, noEnd, allDay}

	at := func(v string) time.Time { x, _ := time.Parse(time.RFC3339, v); return x }
	// 20:00 local: timed in progress (upcoming), noEnd already over.
	up, past := split(evs, at("2026-02-14T19:00:00Z"), loc)
	if fmt.Sprint(ids(up)) != "[timed allday]" || len(past) != 1 || past[0].ID != "noend" {
		t.Fatalf("in progress: up=%v past=%v", ids(up), ids(past))
	}
	// The all-day event is upcoming until the end of 7 March, local.
	up, _ = split([]Event{allDay}, at("2026-03-07T22:59:59Z"), loc)
	if len(up) != 1 {
		t.Fatal("all-day over too early")
	}
	up, past = split([]Event{allDay}, at("2026-03-07T23:00:00Z"), loc)
	if len(up) != 0 || len(past) != 1 {
		t.Fatal("all-day not over at local midnight")
	}
}

func ids(evs []Event) []string {
	var out []string
	for _, e := range evs {
		out = append(out, e.ID)
	}
	return out
}

func TestSplitOrderAndTies(t *testing.T) {
	loc := time.UTC
	d := func(day int) time.Time { return time.Date(2026, 6, day, 12, 0, 0, 0, time.UTC) }
	evs := []Event{
		{ID: "3", Name: "B", Start: d(20)}, {ID: "2", Name: "A", Start: d(20)}, {ID: "1", Name: "A", Start: d(20)},
		{ID: "late", Name: "Z", Start: d(25)}, {ID: "early", Name: "Z", Start: d(2)},
		{ID: "p1", Name: "P", Start: d(1)}, {ID: "p2", Name: "P", Start: d(5)},
	}
	up, past := split(evs, d(10), loc)
	if got := fmt.Sprint(ids(up)); got != "[1 2 3 late]" {
		t.Errorf("upcoming = %s", got)
	}
	if got := fmt.Sprint(ids(past)); got != "[p2 early p1]" {
		t.Errorf("past = %s", got)
	}
}

func pagedHost(t *testing.T, pages int, calls *int, failFrom int) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		if fn != "entities.list" {
			t.Fatalf("host call %q", fn)
		}
		a := args.(map[string]any)
		if a["status"] != "published" || a["schema_slug"] != "event" || a["limit"] != 200 {
			t.Fatalf("args = %v", a)
		}
		*calls++
		if failFrom > 0 && *calls >= failFrom {
			return errors.New("boom")
		}
		next := ""
		if *calls < pages {
			next = fmt.Sprintf("c%d", *calls)
		}
		if *calls > 1 && a["cursor"] != fmt.Sprintf("c%d", *calls-1) {
			t.Fatalf("cursor = %v on call %d", a["cursor"], *calls)
		}
		body := fmt.Sprintf(`{"items":[
			{"id":"p%d","name":"E","status":"published","attributes":{"start_at":"2026-05-01"}},
			{"id":"x%d","name":"Bad","status":"published","attributes":{"start_at":"nope"}}],"next_cursor":%q}`, *calls, *calls, next)
		return json.Unmarshal([]byte(body), out)
	}
}

func TestLoadEventsPaging(t *testing.T) {
	s := defaultSettings()
	calls := 0
	pagedHost(t, 3, &calls, 0)
	evs, err := loadEvents(s)
	if err != nil || len(evs) != 3 || calls != 3 {
		t.Fatalf("evs=%d calls=%d err=%v", len(evs), calls, err)
	}
	calls = 0
	pagedHost(t, 99, &calls, 0)
	evs, err = loadEvents(s)
	if err != nil || calls != maxListPages || len(evs) != maxListPages {
		t.Fatalf("cap: evs=%d calls=%d err=%v", len(evs), calls, err)
	}
}

func TestLoadEventsHostErrors(t *testing.T) {
	s := defaultSettings()
	calls := 0
	pagedHost(t, 9, &calls, 1)
	if _, err := loadEvents(s); err == nil {
		t.Fatal("first page error swallowed")
	}
	calls = 0
	pagedHost(t, 9, &calls, 3)
	evs, err := loadEvents(s)
	if err != nil || len(evs) != 2 {
		t.Fatalf("partial: evs=%d err=%v", len(evs), err)
	}
}
