package main

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func callPage(t *testing.T, pattern, slug string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "GET", Pattern: pattern, PathValues: map[string]string{"slug": slug}})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var r sdk.HTTPResponse
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

const (
	pgVenue = `{"id":"v1","schema_slug":"calendar-venue","slug":"mill","name":"The <script>alert(1)</script> Mill","status":"published",
 "attributes":{"address":"12 High St","city":"Leeds","postcode":"LS1","country":"UK","phone":"+44 1","website":"https://mill.example/?a=1&b=2","latitude":53.8,"longitude":-1.55}}`
	pgOrganizer = `{"id":"o1","schema_slug":"calendar-organizer","slug":"guild","name":"Guild & Co","status":"published",
 "attributes":{"email":"hi@guild.example","phone":"+44 2","website":"javascript:alert(1)"}}`
	pgSeries = `{"id":"s1","schema_slug":"calendar-series","slug":"jazz","name":"Jazz Nights","status":"published",
 "attributes":{"description":"Every <b>week</b>.\nSecond line","image":"/media/jazz.png"}}`
	pgDraft = `{"id":"v2","schema_slug":"calendar-venue","slug":"hidden","name":"Hidden","status":"draft","attributes":{}}`

	pgRows = `[{"id":"f1","schema_slug":"event","slug":"a","name":"Gig <1>","url":"/events/a","starts_at":"2026-07-01T17:00:00+00:00","ends_at":"2026-07-01T19:00:00+00:00"},
 {"id":"f1","schema_slug":"event","slug":"a","name":"Gig <1>","url":"/events/a","starts_at":"2026-07-08T17:00:00+00:00","ends_at":"2026-07-08T19:00:00+00:00"},
 {"id":"f2","schema_slug":"event","slug":"b","name":"Other","url":"javascript:alert(1)","starts_at":"2026-07-09T17:00:00+00:00","ends_at":"2026-07-09T19:00:00+00:00"},
 {"id":"z0","schema_slug":"post","slug":"p","name":"Stale type","url":"/p","starts_at":"2026-07-10T10:00:00+00:00","ends_at":"2026-07-10T10:00:00+00:00"}]`
)

func TestPagesRenderEscapedHTMLWithCacheHeader(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue, pgOrganizer, pgSeries}, dbRows: pgRows}
	h.install(t)
	for _, c := range []struct {
		pattern, slug string
		want          []string
	}{
		{"/venue/{slug}", "mill", []string{
			"<h1>The &lt;script&gt;alert(1)&lt;/script&gt; Mill</h1>", "12 High St<br>Leeds, LS1<br>UK", "+44 1",
			`<a href="https://mill.example/?a=1&amp;b=2">`,
			"Gig &lt;1&gt;</a>", `<a href="/events/a">`,
		}},
		{"/organizer/{slug}", "guild", []string{"<h1>Guild &amp; Co</h1>"}},
		{"/series/{slug}", "jazz", []string{"<h1>Jazz Nights</h1>", "<p>Every &lt;b&gt;week&lt;/b&gt;.</p>", "<p>Second line</p>",
			`<img src="/media/jazz.png" alt="">`, `<h3><a href="/events/a">Gig &lt;1&gt;</a></h3>`}},
	} {
		r := callPage(t, c.pattern, c.slug)
		if r.Status != 200 || r.Headers["Content-Type"] != "text/html; charset=utf-8" || r.Headers["Cache-Control"] != "public, max-age=300" {
			t.Fatalf("%s: status/headers = %d %v", c.slug, r.Status, r.Headers)
		}
		for _, want := range c.want {
			if !strings.Contains(r.Body, want) {
				t.Errorf("%s: missing %q in\n%s", c.slug, want, r.Body)
			}
		}
		for _, bad := range []string{"<script", "<b>", "javascript:", "Stale type", " style=", "onclick"} {
			if strings.Contains(r.Body, bad) {
				t.Errorf("%s: unexpected %q in\n%s", c.slug, bad, r.Body)
			}
		}
	}
	// A series lists the dates of one event under one heading.
	if body := callPage(t, "/series/{slug}", "jazz").Body; strings.Count(body, "<h3>") != 2 || strings.Count(body, "Gig &lt;1&gt;") != 1 {
		t.Errorf("series grouping:\n%s", body)
	}
	// The venue map link only shows when the site shows maps.
	if strings.Contains(callPage(t, "/venue/{slug}", "mill").Body, "openstreetmap") {
		t.Errorf("map link without show_map")
	}
}

func TestPagesAskTheIndexForTheirOwnEvents(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue, pgOrganizer, pgSeries}, dbRows: pgRows}
	h.install(t)
	base := callHost
	var args []any
	callHost = func(fn string, a, out any) error {
		if fn == "db.query" {
			args = a.(map[string]any)["args"].([]any)
		}
		return base(fn, a, out)
	}
	t.Cleanup(func() { callHost = base })
	for _, c := range []struct {
		pattern, slug string
		idx           int // series, organizer, venue = args 12, 13, 14
		id            string
	}{{"/series/{slug}", "jazz", 12, "s1"}, {"/organizer/{slug}", "guild", 13, "o1"}, {"/venue/{slug}", "mill", 14, "v1"}} {
		callPage(t, c.pattern, c.slug)
		for i := 12; i < 15; i++ {
			want := ""
			if i == c.idx {
				want = c.id
			}
			if args[i] != want {
				t.Errorf("%s: arg %d = %v, want %q", c.slug, i, args[i], want)
			}
		}
	}
}

func TestPagesNotFound(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue, pgDraft, pgOrganizer}, dbRows: pgRows}
	h.install(t)
	for _, c := range []struct{ pattern, slug string }{
		{"/venue/{slug}", "nowhere"},
		{"/venue/{slug}", "hidden"},   // not published
		{"/venue/{slug}", "guild"},    // an organizer is not a venue
		{"/organizer/{slug}", "mill"}, // nor the other way round
		{"/series/{slug}", "jazz"},    // no such series
		{"/venue/{slug}", "Bad_Slug"}, // not a slug
		{"/venue/{slug}", "../etc"},   // not a slug
		{"/venue/{slug}", ""},
	} {
		r := callPage(t, c.pattern, c.slug)
		if r.Status != 404 || r.Headers["Cache-Control"] != "public, max-age=60" {
			t.Errorf("%s %q: status = %d, headers = %v", c.pattern, c.slug, r.Status, r.Headers)
		}
	}
}

func TestPagesHostFailureIs503(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue}, getErr: &sdk.HostError{Code: sdk.ErrInternal, Message: "secret internal detail"}}
	h.install(t)
	r := callPage(t, "/venue/{slug}", "mill")
	if r.Status != 503 || strings.Contains(r.Body, "secret") || r.Headers["Content-Type"] != "text/plain; charset=utf-8" {
		t.Fatalf("response = %+v", r)
	}
}

func TestPagesWithoutIndexStillRender(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue}} // db.query fails
	h.install(t)
	r := callPage(t, "/venue/{slug}", "mill")
	if r.Status != 200 || !strings.Contains(r.Body, "Dates are not available yet.") {
		t.Fatalf("response = %+v", r)
	}
	h = &fakeHost{entities: []string{pgVenue}, dbRows: `[]`}
	h.install(t)
	if r := callPage(t, "/venue/{slug}", "mill"); !strings.Contains(r.Body, "No upcoming events.") {
		t.Errorf("empty: %s", r.Body)
	}
}

func TestOrganizerContactNeedsShowContact(t *testing.T) {
	for name, c := range map[string]struct {
		attr string
		show bool
	}{
		"missing": {"", false},
		"false":   {`"show_contact":false,`, false},
		"true":    {`"show_contact":true,`, true},
	} {
		org := strings.Replace(pgOrganizer, `"attributes":{`, `"attributes":{`+c.attr, 1)
		(&fakeHost{entities: []string{org}, dbRows: pgRows}).install(t)
		body := callPage(t, "/organizer/{slug}", "guild").Body
		for _, v := range []string{"hi@guild.example", "+44 2"} {
			if got := strings.Contains(body, v); got != c.show {
				t.Errorf("%s: %q shown = %v, want %v", name, v, got, c.show)
			}
		}
	}
}

func TestPagesCarryOneStaticStyle(t *testing.T) {
	h := &fakeHost{entities: []string{pgVenue, pgOrganizer, pgSeries}, dbRows: pgRows}
	h.install(t)
	for _, c := range []struct{ pattern, slug string }{{"/venue/{slug}", "mill"}, {"/organizer/{slug}", "guild"}, {"/series/{slug}", "jazz"}} {
		body := callPage(t, c.pattern, c.slug).Body
		if strings.Count(body, "<style>") != 1 || strings.Count(body, "</style>") != 1 {
			t.Fatalf("%s: want exactly one style:\n%s", c.slug, body)
		}
		head := body[:strings.Index(body, "</head>")]
		if !strings.Contains(head, "<style>"+pageCSS+"</style>") {
			t.Errorf("%s: style is not pageCSS inside head", c.slug)
		}
	}
	for _, bad := range []string{"url(", "@import", "expression(", "<"} {
		if strings.Contains(pageCSS, bad) {
			t.Errorf("pageCSS contains %q", bad)
		}
	}
}

func TestVenueUpcomingMore(t *testing.T) {
	rows := func(n int) string {
		var parts []string
		for i := 0; i < n; i++ {
			d := 10 + i
			parts = append(parts, `{"id":"f`+strings.Repeat("x", i)+`","schema_slug":"event","slug":"a","name":"Gig","url":"/events/a","starts_at":"2026-07-`+
				strconv.Itoa(d)+`T17:00:00+00:00","ends_at":"2026-07-`+strconv.Itoa(d)+`T19:00:00+00:00"}`)
		}
		return "[" + strings.Join(parts, ",") + "]"
	}
	h := &fakeHost{entities: []string{pgVenue}, dbRows: rows(12)}
	h.install(t)
	body := callPage(t, "/venue/{slug}", "mill").Body
	if !strings.Contains(body, "<h2>Upcoming here</h2>") || !strings.Contains(body, "More (2)") {
		t.Fatalf("12 rows:\n%s", body)
	}
	if before := body[:strings.Index(body, "<details>")]; strings.Count(before, "<li>") != 10 {
		t.Errorf("want 10 items before More:\n%s", before)
	}
	h = &fakeHost{entities: []string{pgVenue}, dbRows: rows(3)}
	h.install(t)
	if body := callPage(t, "/venue/{slug}", "mill").Body; strings.Contains(body, "<details>") {
		t.Errorf("3 rows must not have More:\n%s", body)
	}
}

const pgWeekly = `{"id":"f1","schema_slug":"calendar-event","slug":"a","name":"Gig <1>","status":"published","attributes":{
"start_at":"2026-06-04T18:00","end_at":"2026-06-04T19:00","repeat":"weekly","repeat_weekdays":["th"],"repeat_count":4,
"repeat_skip_dates":["2026-06-11"],"repeat_overrides":["2026-06-18|2026-06-19T19:00"]}}`

func TestSeriesPageSummary(t *testing.T) {
	h := &fakeHost{entities: []string{pgSeries, pgWeekly}, dbRows: pgRows}
	h.install(t)
	body := callPage(t, "/series/{slug}", "jazz").Body
	for _, want := range []string{"Weekly, Thu", "3 dates, 1 changed", `<li class="skipped">`, ">changed</span>"} {
		if !strings.Contains(body, want) {
			t.Errorf("missing %q in\n%s", want, body)
		}
	}
	if strings.Count(body, `class="skipped"`) != 1 || strings.Count(body, ">changed</span>") != 1 {
		t.Errorf("skipped/changed marks:\n%s", body)
	}
	if strings.Count(body, "<h3>") != 2 {
		t.Errorf("want one heading per event:\n%s", body)
	}
}

func TestSeriesPageBudgetFallsBackToTheIndex(t *testing.T) {
	h := &fakeHost{entities: []string{pgSeries, pgWeekly}, dbRows: pgRows}
	h.install(t)
	at := time.Now()
	clock = func() time.Time { at = at.Add(pageFillBudget); return at } // every look burns the budget
	t.Cleanup(func() { clock = time.Now })
	body := callPage(t, "/series/{slug}", "jazz").Body
	for _, call := range h.getCalls {
		if call["id"] != nil {
			t.Errorf("event read past the budget: %v", call)
		}
	}
	if strings.Contains(body, "dates,") || strings.Count(body, "<li>") != 3 {
		t.Errorf("want the index list:\n%s", body)
	}
}

// TestSeriesPageSummaryCapped: a rule with more dates than a page lists must
// not read as if the listed count were the total.
func TestSeriesPageSummaryCapped(t *testing.T) {
	start := time.Now().AddDate(0, 0, 1).Format("2006-01-02") + "T18:00"
	daily := `{"id":"f1","schema_slug":"calendar-event","slug":"a","name":"Daily","status":"published","attributes":{
"start_at":"` + start + `","repeat":"daily"}}`
	h := &fakeHost{entities: []string{pgSeries, daily}, dbRows: pgRows}
	h.install(t)
	body := callPage(t, "/series/{slug}", "jazz").Body
	if !strings.Contains(body, "next 100 dates, 0 changed") {
		t.Errorf("want a capped label:\n%s", body)
	}
}
