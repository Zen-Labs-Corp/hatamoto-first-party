package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func runBuilder(t *testing.T, req sdk.BuilderDataRequest) sdk.BuilderDataRequest {
	t.Helper()
	in, _ := json.Marshal(req)
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var got sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

func titles(r sdk.BuilderDataResult) []string {
	var out []string
	for _, it := range r.Items {
		out = append(out, it.Title)
	}
	return out
}

func ev(id, slug, name, status, locale, start string) string {
	return `{"id":"` + id + `","schema_slug":"event","slug":"` + slug + `","name":"` + name + `","status":"` + status +
		`","locale":"` + locale + `","attributes":{"start_at":"` + start + `"}}`
}

func TestBuilderUpcomingAndPast(t *testing.T) {
	(&fakeHost{entities: []string{
		ev("1", "b", "B", "published", "", "2026-09-01T10:00"),
		ev("2", "a", "A", "published", "", "2026-07-01T10:00"),
		ev("3", "p1", "P1", "published", "", "2026-01-01T10:00"),
		ev("4", "p2", "P2", "published", "", "2026-03-01T10:00"),
		ev("5", "d", "Draft", "draft", "", "2026-08-01T10:00"),
		evFuture,
	}}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{
		{ID: "u", Source: sourceUpcoming}, {ID: "p", Source: sourcePast}, {ID: "x", Source: "ext.other.thing"},
	}})
	if len(got.Results) != 2 {
		t.Fatalf("results = %+v", got.Results)
	}
	if g := strings.Join(titles(got.Results[0]), "|"); got.Results[0].ID != "u" || g != "A|Future, show|B" {
		t.Errorf("upcoming = %s", g)
	}
	if g := strings.Join(titles(got.Results[1]), "|"); got.Results[1].ID != "p" || g != "P2|P1" {
		t.Errorf("past = %s", g)
	}
}

func TestBuilderItemMapping(t *testing.T) {
	(&fakeHost{entities: []string{evFuture}}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	it := got.Results[0].Items[0]
	if it.Title != "Future, show" || it.URL != "https://example.com/e/future" || it.PublishedAt != "2026-07-01T19:00:00Z" {
		t.Errorf("item = %+v", it)
	}
	want := map[string]string{
		"when": "Wed 1 Jul, 7:00 to 9:00 pm", "start_at": "2026-07-01T19:00:00Z", "end_at": "2026-07-01T21:00:00Z",
		"all_day": "false", "venue_name": "Hall", "ics_url": "/p/event-calendar/event/future.ics",
	}
	if len(it.Attrs) != len(want) {
		t.Errorf("attrs = %v", it.Attrs)
	}
	for k, v := range want {
		if it.Attrs[k] != v {
			t.Errorf("attr %s = %q, want %q", k, it.Attrs[k], v)
		}
	}
}

func TestBuilderLimitAndLanguage(t *testing.T) {
	(&fakeHost{entities: []string{
		ev("1", "a", "A", "published", "en", "2026-07-01"),
		ev("2", "b", "B", "published", "de", "2026-07-02"),
		ev("3", "c", "C", "published", "", "2026-07-03"),
	}}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Language: "en", Requests: []sdk.BuilderDataQuery{
		{ID: "u", Source: sourceUpcoming}, {ID: "one", Source: sourceUpcoming, Limit: 1}, {ID: "neg", Source: sourceUpcoming, Limit: -5},
	}})
	if g := strings.Join(titles(got.Results[0]), "|"); g != "A|C" {
		t.Errorf("language filter = %s", g)
	}
	if g := strings.Join(titles(got.Results[1]), "|"); g != "A" {
		t.Errorf("limit 1 = %s", g)
	}
	if len(got.Results[2].Items) != 2 {
		t.Errorf("default limit = %d items", len(got.Results[2].Items))
	}
}

func TestBuilderLimitClampedToMax(t *testing.T) {
	var all []string
	for i := 0; i < 60; i++ {
		all = append(all, ev(string(rune('A'+i%26))+string(rune('a'+i/26)), "s", "E", "published", "", "2026-07-01"))
	}
	(&fakeHost{entities: all}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming, Limit: 500}}})
	if n := len(got.Results[0].Items); n != sdk.MaxBuilderDataItems {
		t.Fatalf("items = %d", n)
	}
}

func TestBuilderHostErrorIsEmptyResult(t *testing.T) {
	(&fakeHost{listErr: errors.New("internal detail")}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if len(got.Results) != 1 || len(got.Results[0].Items) != 0 || got.Results[0].Error != "" {
		t.Fatalf("results = %+v", got.Results)
	}
}

func TestBuilderListsFromIndex(t *testing.T) {
	h := &fakeHost{entities: []string{evFuture}, dbRows: idxRows}
	h.install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}, {ID: "p", Source: sourcePast}}})
	if h.listCalls != 0 || len(got.Results) != 2 {
		t.Fatalf("list calls = %d, results = %+v", h.listCalls, got.Results)
	}
	if g := strings.Join(titles(got.Results[0]), "|"); g != "Future, show" { // the row of the entity the host no longer has is dropped
		t.Errorf("upcoming = %s", g)
	}
	// the index has no URL: it comes from the entity, for the events the host knows
	if it := got.Results[0].Items[0]; it.URL != "https://example.com/e/future" {
		t.Errorf("item = %+v", it)
	}
}

func TestBuilderFillStopsWhenBudgetIsUsed(t *testing.T) {
	h := &fakeHost{entities: []string{evFuture}, dbRows: idxRows}
	h.install(t)
	at := time.Now()
	clock = func() time.Time { at = at.Add(builderBudget); return at } // every look at the clock burns the budget
	t.Cleanup(func() { clock = time.Now })
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if len(got.Results[0].Items) == 0 || len(h.getCalls) != 0 {
		t.Fatalf("items = %d, gets = %d", len(got.Results[0].Items), len(h.getCalls))
	}
}

func TestIndexErrorFallsBackToEntities(t *testing.T) {
	h := &fakeHost{entities: []string{evFuture}} // db.query fails
	h.install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if g := strings.Join(titles(got.Results[0]), "|"); g != "Future, show" || h.listCalls == 0 {
		t.Fatalf("upcoming = %s, list calls = %d", g, h.listCalls)
	}
}

func TestBuilderLoadsOncePerRequest(t *testing.T) {
	calls := 0
	(&fakeHost{entities: []string{evFuture}}).install(t)
	inner := callHost
	callHost = func(fn string, a, o any) error {
		if fn == "entities.list" {
			calls++
		}
		return inner(fn, a, o)
	}
	runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}, {ID: "p", Source: sourcePast}}})
	// one listing per request: one call per accepted content type (auto mode)
	if calls != len(settings{}.slugs()) {
		t.Fatalf("entities.list calls = %d", calls)
	}
}

func TestCapString(t *testing.T) {
	got := capString(strings.Repeat("日", 1000))
	if len(got) > sdk.MaxBuilderDataString || !utf8.ValidString(got) {
		t.Fatalf("len %d valid %v", len(got), utf8.ValidString(got))
	}
	if capString("a\xffb") != "ab" {
		t.Error("invalid UTF-8 kept")
	}
}

func TestBuilderKeepsSharedLocale(t *testing.T) {
	(&fakeHost{entities: []string{
		ev("1", "a", "A", "published", "und", "2026-07-01"),
		ev("2", "b", "B", "published", "de", "2026-07-02"),
	}}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Language: "en", Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if g := strings.Join(titles(got.Results[0]), "|"); g != "A" {
		t.Errorf("language filter = %s", g)
	}
}

func TestBuilderStopsPagingAtDeadline(t *testing.T) {
	(&fakeHost{}).install(t)
	start := time.Now()
	clock = func() time.Time { return start }
	t.Cleanup(func() { clock = time.Now })
	inner := callHost
	pages := 0
	callHost = func(fn string, a, o any) error {
		if fn != "entities.list" {
			return inner(fn, a, o)
		}
		pages++
		clock = func() time.Time { return start.Add(builderBudget) }
		return json.Unmarshal([]byte(`{"items":[`+evFuture+`],"next_cursor":"more"}`), o)
	}
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if pages != 1 || len(got.Results[0].Items) != 1 {
		t.Fatalf("pages = %d, items = %d", pages, len(got.Results[0].Items))
	}
}

// An event that names a venue entity has no venue text of its own: the list
// items must still carry the venue's name and address.
func TestBuilderListResolvesVenueEntity(t *testing.T) {
	(&fakeHost{entities: []string{
		`{"id":"e1","schema_slug":"calendar-event","slug":"jazz","name":"Jazz","status":"published","attributes":{"start_at":"2099-07-01T19:00","venue":["v1"]}}`,
		`{"id":"v1","schema_slug":"calendar-venue","slug":"mill","name":"The Old Mill","status":"published","attributes":{"address":"12 High Street","city":"Leeds"}}`,
	}}).install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if len(got.Results[0].Items) != 1 {
		t.Fatalf("items = %+v", got.Results[0].Items)
	}
	a := got.Results[0].Items[0].Attrs
	if a["venue_name"] != "The Old Mill" || a["venue_address"] != "12 High Street, Leeds" {
		t.Errorf("venue attrs = %v", a)
	}
}

func venueGets(h *fakeHost, id string) (n int) {
	for _, c := range h.getCalls {
		if c["id"] == id {
			n++
		}
	}
	return n
}

// Events at one venue must not each read it again: the list and the feed share a
// small time budget.
func TestVenueIsReadOnceForManyEvents(t *testing.T) {
	ev := func(id string) string {
		return `{"id":"` + id + `","schema_slug":"calendar-event","slug":"` + id + `","name":"` + id + `","status":"published","attributes":{"start_at":"2099-07-01T19:00","venue":["v1"]}}`
	}
	venue := `{"id":"v1","schema_slug":"calendar-venue","slug":"mill","name":"The Old Mill","status":"published","attributes":{"address":"12 High Street","city":"Leeds"}}`
	ents := []string{ev("e1"), ev("e2"), ev("e3"), venue}

	h := &fakeHost{entities: ents}
	h.install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	if len(got.Results[0].Items) != 3 || got.Results[0].Items[2].Attrs["venue_name"] != "The Old Mill" {
		t.Fatalf("items = %+v", got.Results[0].Items)
	}
	if n := venueGets(h, "v1"); n != 1 {
		t.Errorf("list: venue read %d times, want 1", n)
	}

	h = &fakeHost{entities: ents}
	h.install(t)
	if r := call(t, "GET", "/events.ics", ""); r.Status != 200 {
		t.Fatalf("feed status = %d", r.Status)
	}
	if n := venueGets(h, "v1"); n != 1 {
		t.Errorf("feed: venue read %d times, want 1", n)
	}
}

// filterEvent is an entity with a venue name and city, a price and a category.
func filterEvent(id, name, venue, city, extra string) string {
	return `{"id":"` + id + `","schema_slug":"event","slug":"` + id + `","name":"` + name + `","status":"published",
 "attributes":{"start_at":"2026-07-01T19:00","venue_name":"` + venue + `","venue_city":"` + city + `"` + extra + `}}`
}

var filterEntities = []string{
	filterEvent("a", "Jazz Night", "Blue Room", "Bonn", `,"price_amount":0,"category":["c1"]`),
	filterEvent("b", "Rock Gig", "Red Hall", "Köln", `,"price_amount":15`),
	filterEvent("c", "Quiz", "Blue Room", "Bonn", `,"price":"Free"`),
	`{"id":"c1","schema_slug":"calendar-category","slug":"jazz","name":"Jazz","status":"published"}`,
}

func filterTitles(t *testing.T, h *fakeHost, params map[string]any) []string {
	t.Helper()
	h.install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming, Params: params}}})
	return titles(got.Results[0])
}

func TestUpcomingFallbackFilters(t *testing.T) {
	tests := []struct {
		name   string
		params map[string]any
		want   string
	}{
		{"none", nil, "Jazz Night|Quiz|Rock Gig"},
		{"category name", map[string]any{"category": "jazz"}, "Jazz Night"},
		{"venue", map[string]any{"venue": "blue room"}, "Jazz Night|Quiz"},
		{"city", map[string]any{"city": "köln"}, "Rock Gig"},
		{"free", map[string]any{"free": true}, "Jazz Night|Quiz"},
		{"combined", map[string]any{"venue": "Blue Room", "free": true, "category": "Jazz"}, "Jazz Night"},
		{"no match", map[string]any{"city": "Paris"}, ""},
	}
	for _, tc := range tests {
		// db.query fails here: the entities are listed and filtered
		if got := strings.Join(filterTitles(t, &fakeHost{entities: filterEntities}, tc.params), "|"); got != tc.want {
			t.Errorf("%s: titles = %q, want %q", tc.name, got, tc.want)
		}
	}
	// the index has no rows yet (NULL): the same fallback
	if got := strings.Join(filterTitles(t, &fakeHost{entities: filterEntities, dbRows: "null"}, map[string]any{"city": "Bonn", "free": true}), "|"); got != "Jazz Night|Quiz" {
		t.Errorf("not ready: titles = %q", got)
	}
}

func TestUpcomingFiltersReachTheIndexQuery(t *testing.T) {
	rows := `[{"id":"a","name":"Jazz Night","starts_at":"2026-07-01T19:00:00+00:00","ends_at":"2026-07-01T20:00:00+00:00","schema_slug":"event"},
		{"id":"b","name":"Rock Gig","starts_at":"2026-07-02T19:00:00+00:00","ends_at":"2026-07-02T20:00:00+00:00","schema_slug":"event"},
		{"id":"c","name":"Quiz","starts_at":"2026-07-03T19:00:00+00:00","ends_at":"2026-07-03T20:00:00+00:00","schema_slug":"event"}]`
	h := &fakeHost{entities: filterEntities, dbRows: rows}
	h.install(t)
	var args []any
	inner := callHost
	callHost = func(fn string, a, o any) error {
		if fn == "db.query" {
			args = a.(map[string]any)["args"].([]any)
		}
		return inner(fn, a, o)
	}
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{
		{ID: "u:p0123456789abcdef", Source: sourceUpcoming, Limit: 2, Params: map[string]any{"category": "Music", "venue": "Hall", "city": "Bonn", "free": true}},
	}})
	// the filters are bound, and the list is cut to the limit after them
	if len(args) != 12 || args[3] != "Music" || args[4] != "Hall" || args[5] != "Bonn" || args[6] != "free" || args[10] != false {
		t.Errorf("query args = %v", args)
	}
	if got.Results[0].ID != "u:p0123456789abcdef" || strings.Join(titles(got.Results[0]), "|") != "Jazz Night|Rock Gig" {
		t.Errorf("result = %+v", got.Results[0])
	}
	// past lists run latest first
	runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "p", Source: sourcePast, Params: map[string]any{"city": "Bonn"}}}})
	if args[10] != true || args[5] != "Bonn" {
		t.Errorf("past args = %v", args)
	}
}

func TestBuilderWithoutParamsIsUnchanged(t *testing.T) {
	for _, db := range []string{"", idxRows} {
		var out []string
		for _, params := range []map[string]any{nil, {}, {"unknown": "x", "free": false, "city": ""}} {
			(&fakeHost{entities: []string{evFuture, evPast}, dbRows: db}).install(t)
			got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{
				{ID: "u:p0123456789abcdef", Source: sourceUpcoming, Params: params}, {ID: "p", Source: sourcePast, Params: params}}})
			b, _ := json.Marshal(got.Results)
			out = append(out, string(b))
		}
		if out[0] != out[1] || out[0] != out[2] || !strings.Contains(out[0], "u:p0123456789abcdef") || !strings.Contains(out[0], "Future, show") {
			t.Errorf("db=%v: results differ: %v", db != "", out)
		}
	}
}

func TestBuilderReadsARepeatingEventOnceAndKeepsEachDate(t *testing.T) {
	h := &fakeHost{entities: []string{evFuture}, dbRows: repeatingRows}
	h.install(t)
	got := runBuilder(t, sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "u", Source: sourceUpcoming}}})
	items := got.Results[0].Items
	if len(items) != 3 || venueGets(h, "f1") != 1 {
		t.Fatalf("items = %d, entity reads = %d", len(items), venueGets(h, "f1"))
	}
	for i, want := range []string{"2026-07-01T17:00:00Z", "2026-07-08T17:00:00Z", "2026-07-15T17:00:00Z"} {
		if items[i].PublishedAt != want || items[i].URL != "https://example.com/e/future" {
			t.Errorf("item %d = %+v", i, items[i])
		}
	}
}
