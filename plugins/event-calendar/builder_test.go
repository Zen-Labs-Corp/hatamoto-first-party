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
	if g := strings.Join(titles(got.Results[0]), "|"); g != "Future, show|Indexed only" {
		t.Errorf("upcoming = %s", g)
	}
	// the index has no URL: it comes from the entity, for the events the host knows
	if it := got.Results[0].Items[0]; it.URL != "https://example.com/e/future" {
		t.Errorf("item = %+v", it)
	}
	if got.Results[0].Items[1].URL != "" {
		t.Errorf("event unknown to the host got a URL: %+v", got.Results[0].Items[1])
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
