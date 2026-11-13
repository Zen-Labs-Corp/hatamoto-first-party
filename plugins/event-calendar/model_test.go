package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
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

// legacySettings reads only the older event type.
func legacySettings() settings {
	s := defaultSettings()
	s.SchemaSlug = legacySchemaSlug
	return s
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
	s := legacySettings()
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
	s := legacySettings()
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

// evOnType is a published event entity of the given content type.
func evOnType(id, slug, attrs string) string {
	return `{"id":"` + id + `","schema_slug":"` + slug + `","slug":"` + id + `","name":"` + id + `","status":"published","attributes":` + attrs + `}`
}

func TestSchemaSlugAuto(t *testing.T) {
	h := &fakeHost{entities: []string{
		evOnType("new", "calendar-event", `{"start_at":"2026-07-01T19:00"}`),
		evOnType("old", "event", `{"start_at":"2026-07-02T19:00"}`),
		evOnType("ven", "venue_event", `{"start_at":"2026-07-03T19:00"}`),
	}}
	h.install(t)
	var listed []any
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == "entities.list" {
			listed = append(listed, args.(map[string]any)["schema_slug"])
		}
		return inner(fn, args, out)
	}
	ids := func(s settings) string {
		evs, err := loadEvents(s)
		if err != nil {
			t.Fatal(err)
		}
		var got []string
		for _, e := range evs {
			got = append(got, e.ID)
		}
		return fmt.Sprint(got)
	}
	if got := ids(settings{TimeZone: "UTC"}); got != "[new old]" {
		t.Errorf("auto = %s", got)
	}
	if fmt.Sprint(listed) != "[calendar-event event]" {
		t.Errorf("auto listed %v", listed)
	}
	listed = nil
	if got := ids(settings{SchemaSlug: "venue_event", TimeZone: "UTC"}); got != "[ven]" {
		t.Errorf("explicit = %s", got)
	}
	if fmt.Sprint(listed) != "[venue_event]" {
		t.Errorf("explicit listed %v", listed)
	}
}

func TestEachEventKeepsFirstSlugWhenSecondFails(t *testing.T) {
	calls := 0
	callHost = func(fn string, args, out any) error {
		calls++
		if args.(map[string]any)["schema_slug"] == "event" {
			return errors.New("down")
		}
		return json.Unmarshal([]byte(`{"items":[`+evOnType("new", "calendar-event", `{"start_at":"2026-07-01T19:00"}`)+`]}`), out)
	}
	evs, err := loadEvents(settings{TimeZone: "UTC"})
	if err != nil || len(evs) != 1 {
		t.Fatalf("events = %v, err = %v", evs, err)
	}
	callHost = func(string, any, any) error { return errors.New("down") }
	if _, err := loadEvents(settings{TimeZone: "UTC"}); err == nil {
		t.Error("first-page error swallowed")
	}
}

func TestEachEventPageCapIsShared(t *testing.T) {
	pages := 0
	callHost = func(fn string, args, out any) error {
		pages++
		return json.Unmarshal([]byte(`{"items":[],"next_cursor":"more"}`), out)
	}
	if _, err := loadEvents(settings{TimeZone: "UTC"}); err != nil || pages != maxListPages {
		t.Fatalf("pages = %d, err = %v", pages, err)
	}
}

func TestEventFromEntityNewFields(t *testing.T) {
	s := settings{TimeZone: "UTC"}
	raw := entity("n", "Gala", "published", `{"start_at":"2026-02-14T19:00","end_at":"2026-02-14T21:00","time_zone":"Europe/Berlin",
		"venue":[" v1 ","v2"],"organizers":["o1","","o2"],"category":["c1"],"series":["s1"],"venue_city":"Bonn",
		"price":"Free","price_amount":"0","currency":"EUR","featured":true,"online_url":"https://example.org/join",
		"website":"https://example.org","event_status":"cancelled","attendance_mode":"hybrid",
		"source_kind":"ics","source_uid":"u1","source_url":"https://example.org/f.ics"}`)
	raw.SchemaSlug = "calendar-event"
	e, ok := eventFromEntity(raw, s)
	if !ok {
		t.Fatal("rejected")
	}
	if e.VenueID != "v1" || e.SeriesID != "s1" || fmt.Sprint(e.OrganizerIDs) != "[o1 o2]" || fmt.Sprint(e.CategoryIDs) != "[c1]" ||
		e.VenueCity != "Bonn" || e.Price != "Free" || e.PriceAmount == nil || *e.PriceAmount != 0 || e.Currency != "EUR" ||
		!e.Featured || e.OnlineURL == "" || e.Website == "" || e.EventStatus != "cancelled" || e.AttendanceMode != "hybrid" ||
		e.SourceKind != "ics" || e.SourceUID != "u1" || e.SourceURL == "" || e.SchemaSlug != "calendar-event" || e.TimeZone != "Europe/Berlin" {
		t.Fatalf("event = %+v", e)
	}
	// 19:00 in Berlin (UTC+1 in February) is 18:00 UTC, not 19:00.
	if got := e.Start.UTC().Format(time.RFC3339); got != "2026-02-14T18:00:00Z" {
		t.Errorf("start = %s", got)
	}
	plain, _ := eventFromEntity(entity("p", "Plain", "published", `{"start_at":"2026-02-14T19:00","time_zone":"No/Such"}`), s)
	if plain.EventStatus != "scheduled" || plain.AttendanceMode != "offline" || plain.PriceAmount != nil ||
		plain.VenueID != "" || plain.Start.UTC().Hour() != 19 {
		t.Errorf("defaults = %+v", plain)
	}
}

func TestAttrIDsCap(t *testing.T) {
	var ids []any
	for i := 0; i < 30; i++ {
		ids = append(ids, fmt.Sprint("id", i))
	}
	if got := attrIDs(map[string]any{"k": ids}, "k"); len(got) != maxRefIDs {
		t.Errorf("ids = %d", len(got))
	}
	if attrIDs(map[string]any{"k": "x"}, "k") != nil {
		t.Error("non-array accepted")
	}
}

// An all-day event keeps its calendar days in its own zone: on a site whose
// zone is behind it, no renderer may show the previous day.
func TestAllDayEventWithOwnZoneKeepsItsDay(t *testing.T) {
	s := settings{TimeZone: "Europe/London"}
	raw := entityDTO{ID: "a1", SchemaSlug: "calendar-event", Slug: "fest", Name: "Fest", Status: "published",
		Attributes: []byte(`{"start_at":"2026-02-14","end_at":"2026-02-15","all_day":true,"time_zone":"Europe/Berlin"}`)}
	e, ok := eventFromEntity(raw, s)
	if !ok {
		t.Fatal("not an event")
	}
	loc := s.location()
	at := time.Date(2026, 2, 1, 12, 0, 0, 0, time.UTC)

	if got := toItem(e, s, at); got.Attrs["start_at"][:10] != "2026-02-14" || got.Attrs["end_at"][:10] != "2026-02-15" ||
		!strings.Contains(got.Attrs["when"], "14") || strings.Contains(got.Attrs["when"], "13") {
		t.Errorf("list item = %v", got.Attrs)
	}
	ics := writeCalendar([]Event{e}, at, loc)
	if !strings.Contains(ics, "DTSTART;VALUE=DATE:20260214\r\n") || !strings.Contains(ics, "DTEND;VALUE=DATE:20260216\r\n") {
		t.Errorf("ics = %q", ics)
	}
	if g := calendarLinks(e, dateLocation(e, loc), "", "").Google; !strings.Contains(g, "dates=20260214%2F20260216") {
		t.Errorf("google link = %q", g)
	}
	if got := isoTime(e.Start, e.AllDay, dateLocation(e, loc)); got != "2026-02-14" {
		t.Errorf("json-ld start = %q", got)
	}
	// over at the end of the 15th in Berlin
	if !e.endsAt(loc).Equal(time.Date(2026, 2, 15, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("endsAt = %v", e.endsAt(loc))
	}
}

// The events table has CHECKs on both fields: a value outside them must never
// reach it, or the row cannot be written.
func TestEventStatusAndAttendanceModeAreNormalised(t *testing.T) {
	for _, c := range []struct{ in, status, mode string }{
		{`"event_status":"Cancelled","attendance_mode":"HYBRID"`, "cancelled", "hybrid"},
		{`"event_status":"rescheduled","attendance_mode":"mixed"`, "scheduled", "offline"},
		{`"event_status":" postponed "`, "postponed", "offline"},
	} {
		raw := entityDTO{ID: "e", Status: "published", Attributes: []byte(`{"start_at":"2026-07-01",` + c.in + `}`)}
		e, ok := eventFromEntity(raw, settings{})
		if !ok || e.EventStatus != c.status || e.AttendanceMode != c.mode {
			t.Errorf("%s -> %q, %q", c.in, e.EventStatus, e.AttendanceMode)
		}
	}
}

func TestEventFromEntityRepeatFields(t *testing.T) {
	s := settings{SchemaSlug: "event", TimeZone: "UTC", DateFormat: "short"}
	parse := func(extra string) Repeat {
		t.Helper()
		e, ok := eventFromEntity(entity("r", "R", "published", `{"start_at":"2026-02-03T19:00",`+extra+`}`), s)
		if !ok {
			t.Fatalf("event rejected: %s", extra)
		}
		return e.Repeat
	}
	day := func(d string) time.Time {
		return time.Date(2026, 2, 1, 0, 0, 0, 0, time.UTC).AddDate(0, 0, mustAtoi(t, d)-1)
	}

	r := parse(`"repeat":"monthly","repeat_every":2,"repeat_weekdays":["mo","TU","mo","xx",""],` +
		`"repeat_by":"nth_weekday","repeat_nth":-1,"repeat_until":"2026-12-31","repeat_count":5,` +
		`"repeat_extra_dates":["2026-02-10","bad"],"repeat_skip_dates":["2026-02-17"],` +
		`"repeat_overrides":["2026-02-24|2026-02-25T10:00|2026-02-25T11:00","2026-03-03|2026-03-03T08:00","nope|x","2026-03-10","2026-12-01|19:00|20:00","2026-12-08|9:00"]`)
	if r.Kind != "monthly" || r.Every != 2 || r.By != "nth_weekday" || r.Nth != -1 || r.Count != 5 ||
		len(r.Weekdays) != 2 || r.Weekdays[0] != time.Monday || r.Weekdays[1] != time.Tuesday ||
		!r.Until.Equal(time.Date(2026, 12, 31, 0, 0, 0, 0, time.UTC)) ||
		len(r.Extra) != 1 || !r.Extra[0].Equal(day("10")) || len(r.Skip) != 1 || !r.Skip[0].Equal(day("17")) {
		t.Fatalf("repeat = %+v", r)
	}
	if o := r.Over["2026-02-24"]; o.Start != "2026-02-25T10:00" || o.End != "2026-02-25T11:00" {
		t.Errorf("override = %+v", o)
	}
	if o, ok := r.Over["2026-03-03"]; !ok || o.Start != "2026-03-03T08:00" || o.End != "" {
		t.Errorf("override without end = %+v, %v", o, ok)
	}
	if o, ok := r.Over["2026-03-10"]; !ok || o.Start != "" {
		t.Errorf("override with only a date = %+v, %v", o, ok)
	}
	if o := r.Over["2026-12-01"]; o.Start != "2026-12-01T19:00" || o.End != "2026-12-01T20:00" {
		t.Errorf("short-form override = %+v", o)
	}
	if o := r.Over["2026-12-08"]; o.Start != "2026-12-08T09:00" || o.End != "" {
		t.Errorf("short-form override, one-digit hour, no end = %+v", o)
	}
	if len(r.Over) != 5 {
		t.Errorf("overrides = %v", r.Over)
	}

	// Numbers may arrive as strings; bad values fall back, never fail.
	r = parse(`"repeat":"weekly","repeat_every":"3","repeat_count":"4","repeat_nth":"2"`)
	if r.Kind != "weekly" || r.Every != 3 || r.Count != 4 || r.Nth != 2 {
		t.Errorf("numeric strings = %+v", r)
	}
	r = parse(`"repeat":"daily","repeat_every":0,"repeat_count":-2,"repeat_nth":9,"repeat_until":"soon","repeat_by":"x"`)
	if r.Kind != "daily" || r.Every != 1 || r.Count != 0 || r.Nth != 0 || !r.Until.IsZero() || r.By != "day_of_month" {
		t.Errorf("bad values = %+v", r)
	}
	for _, kind := range []string{`"repeat":"hourly"`, `"repeat":"none"`, `"repeat":5`, `"other":1`} {
		if r := parse(kind); r.Kind != "none" || r.Every != 1 {
			t.Errorf("%s = %+v", kind, r)
		}
	}

	// More than maxRefIDs dates are all kept.
	var dates []string
	for i := 0; i < 30; i++ {
		dates = append(dates, fmt.Sprintf(`"2026-05-%02d"`, i+1))
	}
	if r := parse(`"repeat":"daily","repeat_skip_dates":[` + strings.Join(dates, ",") + `]`); len(r.Skip) != 30 {
		t.Errorf("skip dates = %d", len(r.Skip))
	}
}

func mustAtoi(t *testing.T, s string) int {
	t.Helper()
	n, err := strconv.Atoi(s)
	if err != nil {
		t.Fatal(err)
	}
	return n
}
