package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const apiPath = "/api/events"

// apiHost serves rows from the index and records the last range query's args.
type apiHost struct {
	fakeHost
	settings string
	args     []any
	queries  int
}

func (h *apiHost) install(t *testing.T) {
	t.Helper()
	h.fakeHost.install(t)
	inner := callHost
	callHost = func(fn string, a, o any) error {
		switch fn {
		case "settings.get":
			if h.settings != "" {
				return json.Unmarshal([]byte(h.settings), o)
			}
		case "db.query":
			h.queries++
			h.args = a.(map[string]any)["args"].([]any)
		}
		return inner(fn, a, o)
	}
}

func getAPI(t *testing.T, h *apiHost, q map[string][]string) (sdk.HTTPResponse, apiResponse) {
	t.Helper()
	h.install(t)
	r := callQuery(t, http.MethodGet, apiPath, q)
	var body apiResponse
	if r.Status == http.StatusOK {
		if err := json.Unmarshal([]byte(r.Body), &body); err != nil {
			t.Fatalf("body is not JSON: %v: %s", err, r.Body)
		}
	}
	return r, body
}

func q1(kv ...string) map[string][]string {
	m := map[string][]string{}
	for i := 0; i < len(kv); i += 2 {
		m[kv[i]] = []string{kv[i+1]}
	}
	return m
}

const apiRows = `[
 {"id":"a","schema_slug":"calendar-event","name":"Jazz","slug":"jazz","url":"/event/jazz","starts_at":"2026-06-10T17:00:00+00:00","ends_at":"2026-06-10T19:30:00+00:00",
  "venue_name":"Blue Room","venue_slug":"blue-room","venue_city":"Bonn","venue_address":"1 Main St, Bonn","lat":50.7,"lon":7.1,
  "category_names":["Music"],"price":"","price_amount":0,"featured":true,"image":"/img/a.jpg","event_status":"cancelled"},
 {"id":"b","schema_slug":"event","name":"Fair","slug":"fair","starts_at":"2026-06-10T22:00:00+00:00","ends_at":"2026-06-13T22:00:00+00:00","all_day":true,
  "time_zone":"Europe/Berlin","category_names":null,"price":"5 EUR","lat":null,"lon":null},
 {"id":"c","schema_slug":"event","name":"Late","slug":"late","starts_at":"2026-06-12T20:00:00+00:00","ends_at":"2026-06-12T22:00:00+00:00"},
 {"id":"x","schema_slug":"post","name":"Not an event","slug":"x","starts_at":"2026-06-12T20:00:00+00:00","ends_at":"2026-06-12T22:00:00+00:00"}]`

func TestAPIEventsRange(t *testing.T) {
	h := &apiHost{fakeHost: fakeHost{dbRows: apiRows}, settings: `{"time_zone":"Europe/Berlin","week_starts_on":"sunday","show_map":true}`}
	r, body := getAPI(t, h, q1("from", "2026-06-10", "to", "2026-06-20"))
	if r.Status != 200 || r.Headers["Content-Type"] != "application/json; charset=utf-8" || r.Headers["Cache-Control"] != "public, max-age=60" {
		t.Fatalf("response = %d %v", r.Status, r.Headers)
	}
	if _, bad := r.Headers["Set-Cookie"]; bad {
		t.Error("route sets a cookie")
	}
	// dates are midnight in the site zone (UTC+2 in June); [from, to) overlap
	if h.args[0] != "2026-06-09T22:00:00Z" || h.args[1] != "2026-06-19T22:00:00Z" || h.args[9] != maxIndexRows {
		t.Errorf("query args = %v", h.args)
	}
	if !body.Ready || body.TZ != "Europe/Berlin" || body.WeekStart != "sunday" || !body.ShowMap || body.Truncated {
		t.Errorf("envelope = %+v", body)
	}
	if len(body.Events) != 3 { // the row of another content type is dropped
		t.Fatalf("events = %+v", body.Events)
	}
	a, b, c := body.Events[0], body.Events[1], body.Events[2]
	if a.ID != "a" || a.Title != "Jazz" || a.URL != "/event/jazz" || a.Start != "2026-06-10T17:00:00Z" || a.End != "2026-06-10T19:30:00Z" ||
		a.Day != "2026-06-10" || a.EndDay != "2026-06-10" || a.AllDay || a.Venue != "Blue Room" || a.VenueSlug != "blue-room" || a.City != "Bonn" ||
		a.Address != "1 Main St, Bonn" || a.Lat == nil || *a.Lat != 50.7 || a.Lon == nil || a.Price != "Free" || !a.Featured ||
		a.Image != "/img/a.jpg" || a.Status != "cancelled" || len(a.Categories) != 1 || !strings.Contains(a.When, "10 Jun") {
		t.Errorf("timed event = %+v", a)
	}
	// all-day 11 to 13 June in Berlin: the stored end is midnight after the 13th
	if !b.AllDay || b.Day != "2026-06-11" || b.EndDay != "2026-06-13" || b.Lat != nil || b.Lon != nil || b.Price != "5 EUR" ||
		b.Status != "scheduled" || b.Categories == nil || !strings.Contains(b.When, "all day") {
		t.Errorf("all-day event = %+v", b)
	}
	// 22:00 UTC is midnight in Berlin: a timed event ending at midnight ends the day before
	if c.Day != "2026-06-12" || c.EndDay != "2026-06-12" {
		t.Errorf("late event = %+v", c)
	}
	raw := r.Body
	for _, k := range []string{`"lat":null`, `"categories":[]`} {
		if !strings.Contains(raw, k) {
			t.Errorf("body lacks %s", k)
		}
	}
	h.assertSingleSQL(t)
}

func (h *apiHost) assertSingleSQL(t *testing.T) {
	t.Helper()
	if h.queries != 1 {
		t.Errorf("db queries = %d", h.queries)
	}
}

func TestAPIEventsDefaultsCapAndHead(t *testing.T) {
	h := &apiHost{fakeHost: fakeHost{dbRows: `[]`}}
	_, body := getAPI(t, h, nil)
	// now is 2026-06-01 12:00 UTC: a month from now
	if h.args[0] != "2026-06-01T12:00:00Z" || h.args[1] != "2026-07-02T12:00:00Z" || !body.Ready || body.Events == nil || body.WeekStart != "monday" || body.ShowMap {
		t.Errorf("defaults: args %v body %+v", h.args, body)
	}
	if _, body = getAPI(t, h, q1("limit", "9999")); h.args[9] != maxIndexRows {
		t.Errorf("limit = %v", h.args[9])
	}
	getAPI(t, h, q1("limit", "7", "slug", "gala", "to", "2026-06-02T00:00:00Z"))
	if h.args[9] != 7 || h.args[7] != "gala" || h.args[1] != "2026-06-02T00:00:00Z" {
		t.Errorf("args = %v", h.args)
	}
	// truncated when the cap was reached
	h.dbRows = `[{"id":"a","schema_slug":"event","name":"A","starts_at":"2026-06-02T10:00:00Z","ends_at":"2026-06-02T11:00:00Z"}]`
	if _, body = getAPI(t, h, q1("limit", "1")); !body.Truncated || len(body.Events) != 1 {
		t.Errorf("truncated = %+v", body)
	}
	h.install(t)
	if r := callQuery(t, http.MethodHead, apiPath, nil); r.Status != 200 {
		t.Errorf("HEAD = %d", r.Status)
	}
	if r := callQuery(t, http.MethodPost, apiPath, nil); r.Status != 404 {
		t.Errorf("POST = %d", r.Status)
	}
}

func TestAPIEventsFilters(t *testing.T) {
	h := &apiHost{fakeHost: fakeHost{dbRows: `[]`}}
	if r, _ := getAPI(t, h, q1("q", "jazz", "category", "Music", "venue", "hall", "city", "Bonn", "price", "free", "view", "month")); r.Status != 200 {
		t.Fatalf("status = %d", r.Status)
	}
	for i, want := range map[int]string{2: "jazz", 3: "Music", 4: "hall", 5: "Bonn", 6: "free"} {
		if h.args[i] != want {
			t.Errorf("arg %d = %v, want %s", i, h.args[i], want)
		}
	}
	getAPI(t, h, q1("q", "50%_"))
	if h.args[2] != `50\%\_` {
		t.Errorf("q arg = %v", h.args[2])
	}
	getAPI(t, h, q1("price", "any"))
	if h.args[6] != "" {
		t.Errorf("price any = %v", h.args[6])
	}
}

func TestAPIEventsBadRequests(t *testing.T) {
	bad := map[string]map[string][]string{
		"unknown key":    q1("debug", "1"),
		"repeated key":   {"q": {"a", "b"}},
		"oversized":      q1("q", strings.Repeat("x", 201)),
		"range over 400": q1("from", "2026-01-01", "to", "2027-03-01"),
		"to before from": q1("from", "2026-06-10", "to", "2026-06-01"),
		"to equals from": q1("from", "2026-06-10", "to", "2026-06-10"),
		"bad price":      q1("price", "cheap"),
		"bad view":       q1("view", "x"),
		"bad from":       q1("from", "tomorrow"),
		"bad to":         q1("to", "2026-13-40"),
		"bad limit":      q1("limit", "many"),
		"zero limit":     q1("limit", "0"),
		"bad slug":       q1("slug", "../x"),
	}
	for name, q := range bad {
		h := &apiHost{fakeHost: fakeHost{dbRows: `[]`}}
		r, _ := getAPI(t, h, q)
		if r.Status != http.StatusBadRequest || r.Body != "bad request" || h.queries != 0 {
			t.Errorf("%s: status %d body %q queries %d", name, r.Status, r.Body, h.queries)
		}
		for _, v := range q {
			if len(v[0]) > 3 && strings.Contains(r.Body, v[0]) {
				t.Errorf("%s: input echoed", name)
			}
		}
	}
	// 400 days is allowed
	h := &apiHost{fakeHost: fakeHost{dbRows: `[]`}}
	if r, _ := getAPI(t, h, q1("from", "2026-01-01", "to", "2027-02-05")); r.Status != 200 {
		t.Errorf("400-day range = %d", r.Status)
	}
}

func TestAPIEventsFailOpen(t *testing.T) {
	// the index is not reconciled yet (NULL), and the db fails: 200, empty, no error text
	for name, h := range map[string]*apiHost{
		"not ready": {fakeHost: fakeHost{dbRows: "null"}},
		"db error":  {fakeHost: fakeHost{}},
	} {
		r, body := getAPI(t, h, nil)
		if r.Status != 200 || body.Ready || len(body.Events) != 0 || body.Events == nil || r.Headers["Content-Type"] != "application/json; charset=utf-8" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if strings.Contains(r.Body, "no db") {
			t.Errorf("%s: host text in the body", name)
		}
	}
	h := &apiHost{}
	h.install(t)
	inner := callHost
	callHost = func(fn string, a, o any) error {
		if fn == "db.query" {
			return errors.New("secret table detail")
		}
		return inner(fn, a, o)
	}
	if r := callQuery(t, http.MethodGet, apiPath, nil); r.Status != 200 || strings.Contains(r.Body, "secret") {
		t.Errorf("host error leaked: %d %s", r.Status, r.Body)
	}
}
