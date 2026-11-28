package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// seedEvent is one occurrence in the fake events table.
type seedEvent struct {
	ID, Name, Venue, VenueID, Category string
	Start, End                         time.Time
	AllDay                             bool
}

// eventsHost fakes settings and the index tables behind /admin/events. It
// reads the documented positional arguments of each statement and filters,
// sorts and slices the seeded rows the way the SQL does.
type eventsHost struct {
	rows     []seedEvent
	notReady bool
	dbErr    error
	pending  int
	queries  []sqlCall
}

var eventsNow = time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)

// seedEvents makes 900 upcoming and 600 past occurrences, every 10th without a
// venue, three venues, two categories and about twelve a day.
func seedEvents() []seedEvent {
	var rows []seedEvent
	for i := 0; i < 1500; i++ {
		e := seedEvent{ID: fmt.Sprintf("e%04d", i), Name: fmt.Sprintf("Event %04d", i)}
		if i < 900 {
			e.Start = eventsNow.Add(time.Duration(i+1) * 2 * time.Hour)
		} else {
			e.Start = eventsNow.Add(-time.Duration(i-899) * 2 * time.Hour)
		}
		e.End = e.Start.Add(time.Hour)
		if i%10 != 0 {
			e.Venue = []string{"Anchor", "Barn", "Cellar"}[i%3]
			e.VenueID = "v" + e.Venue
		}
		e.Category = []string{"Music", "Talks"}[i%2]
		rows = append(rows, e)
	}
	return rows
}

func (h *eventsHost) install(t *testing.T) {
	t.Helper()
	oldNow := now
	now = func() time.Time { return eventsNow }
	t.Cleanup(func() { now = oldNow })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		switch fn {
		case sdk.FnSettingsGet:
			return json.Unmarshal([]byte(`{"time_zone":"Europe/Berlin"}`), out)
		case sdk.FnDBQuery:
			call := sqlCall{}
			call.sql, _ = a["sql"].(string)
			call.args, _ = a["args"].([]any)
			h.queries = append(h.queries, call)
			if h.dbErr != nil {
				return h.dbErr
			}
			return json.Unmarshal([]byte(`{"rows":[[`+h.cell(t, call)+`]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (h *eventsHost) cell(t *testing.T, c sqlCall) string {
	switch {
	case strings.Contains(c.sql, "'day_counts'"):
		if h.notReady {
			return `null`
		}
		return h.list(t, c)
	case strings.Contains(c.sql, "submissions WHERE status"):
		up, past := 0, 0
		for _, r := range h.rows {
			if r.End.After(eventsNow) {
				up++
			} else {
				past++
			}
		}
		return fmt.Sprintf(`{"upcoming":%d,"past":%d,"repeating":4,"submissions_pending":%d}`, up, past, h.pending)
	case strings.Contains(c.sql, "'venues'"):
		return `{"venues":["Anchor","Barn","Cellar"],"categories":["Music","Talks"]}`
	}
	t.Fatalf("unexpected statement %q", c.sql)
	return ""
}

// list answers eventsListSQL: args are now, past, q, category, venue, novenue,
// day, tz, limit, offset.
func (h *eventsHost) list(t *testing.T, c sqlCall) string {
	past, _ := c.args[1].(bool)
	q, _ := c.args[2].(string)
	cat, _ := c.args[3].(string)
	venue, _ := c.args[4].(string)
	novenue, _ := c.args[5].(bool)
	day, _ := c.args[6].(string)
	loc, err := time.LoadLocation(c.args[7].(string))
	if err != nil {
		t.Fatal(err)
	}
	limit, offset := c.args[8].(int), c.args[9].(int)
	var f []seedEvent
	for _, r := range h.rows {
		switch {
		case past == r.End.After(eventsNow):
		case q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Venue), strings.ToLower(q)):
		case cat != "" && !strings.EqualFold(r.Category, cat):
		case venue != "" && !strings.EqualFold(r.Venue, venue):
		case novenue && r.Venue != "":
		case day != "" && r.Start.In(loc).Format("2006-01-02") != day:
		default:
			f = append(f, r)
		}
	}
	sort.SliceStable(f, func(i, j int) bool {
		if past {
			return f[i].Start.After(f[j].Start)
		}
		return f[i].Start.Before(f[j].Start)
	})
	end := min(offset+limit, len(f))
	page := []seedEvent{}
	if offset < len(f) {
		page = f[offset:end]
	}
	days := map[string]int{}
	onPage := map[string]bool{}
	for _, r := range page {
		onPage[r.Start.In(loc).Format("2006-01-02")] = true
	}
	for _, r := range f {
		if d := r.Start.In(loc).Format("2006-01-02"); onPage[d] {
			days[d]++
		}
	}
	items := []map[string]any{}
	for _, r := range page {
		items = append(items, map[string]any{
			"id": r.ID, "slug": r.ID, "name": r.Name, "os": r.Start.Format(time.RFC3339), "oe": r.End.Format(time.RFC3339),
			"all_day": r.AllDay, "venue_name": r.Venue, "venue_id": r.VenueID, "venue_city": "",
			"category_names": []string{r.Category}, "price": "", "repeating": false, "source_kind": "",
		})
	}
	b, _ := json.Marshal(map[string]any{"total": len(f), "items": items, "day_counts": days})
	return string(b)
}

func getEvents(t *testing.T, u *sdk.HTTPUser, query map[string][]string) (sdk.HTTPResponse, eventsPage) {
	t.Helper()
	r := callRoute(t, "GET", "/admin/events", u, nil, query)
	var got eventsPage
	if r.Status == 200 {
		if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
			t.Fatal(err)
		}
	}
	return r, got
}

func TestEventsPaging(t *testing.T) {
	h := &eventsHost{rows: seedEvents(), pending: 3}
	h.install(t)
	r, p1 := getEvents(t, owner, map[string][]string{"tab": {"upcoming"}})
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	if !p1.Ready || len(p1.Items) != 50 || p1.Total != 900 || p1.Page != 1 || p1.PerPage != 50 {
		t.Fatalf("page 1 = ready %v items %d total %d page %d per %d", p1.Ready, len(p1.Items), p1.Total, p1.Page, p1.PerPage)
	}
	if p1.Items[0].Name != "Event 0000" || p1.Items[49].Name != "Event 0049" {
		t.Errorf("page 1 runs %s to %s", p1.Items[0].Name, p1.Items[49].Name)
	}
	c := p1.Counts
	if c.Upcoming != 900 || c.Past != 600 || c.SubmissionsPending != 3 || c.Repeating != 4 || c.Drafts != nil {
		t.Errorf("counts = %+v", c)
	}
	_, p2 := getEvents(t, owner, map[string][]string{"page": {"2"}})
	if len(p2.Items) != 50 || p2.Items[0].Name != "Event 0050" || p2.Total != 900 {
		t.Errorf("page 2 starts at %v, total %d", p2.Items[0].Name, p2.Total)
	}
	_, last := getEvents(t, owner, map[string][]string{"page": {"19"}})
	if len(last.Items) != 0 || last.Total != 900 {
		t.Errorf("page 19 = %d items, total %d", len(last.Items), last.Total)
	}
	// the start is in the site's zone: the first event starts at 14:00 UTC on 1 March, 15:00 in Berlin
	if it := p1.Items[0]; it.Start != "2027-03-01T15:00:00+01:00" || it.Time != "15:00" || it.Day != "2027-03-01" || it.Status != "published" {
		t.Errorf("first item = %+v", it)
	}
	if p1.DayCounts["2027-03-01"] == 0 {
		t.Errorf("day counts = %v", p1.DayCounts)
	}
	for d, n := range p1.DayCounts {
		got := 0
		for _, it := range p1.Items {
			if it.Day == d {
				got++
			}
		}
		if n < got {
			t.Errorf("day %s counts %d, page holds %d", d, n, got)
		}
	}
	sql := h.queries[1].sql
	for _, want := range []string{"LIMIT $9", "OFFSET $10", "AT TIME ZONE $8::text", "ORDER BY CASE WHEN $2::boolean"} {
		if !strings.Contains(sql, want) {
			t.Errorf("list statement lacks %q", want)
		}
	}
	if a := h.queries[1].args; a[8] != 50 || a[9] != 0 || a[7] != "Europe/Berlin" {
		t.Errorf("list args = %v", a)
	}
}

func TestEventsPastNewestFirst(t *testing.T) {
	h := &eventsHost{rows: seedEvents()}
	h.install(t)
	_, p := getEvents(t, owner, map[string][]string{"tab": {"past"}})
	if p.Total != 600 || len(p.Items) != 50 {
		t.Fatalf("total %d, items %d", p.Total, len(p.Items))
	}
	if p.Items[0].Name != "Event 0900" || p.Items[1].Name != "Event 0901" {
		t.Errorf("past starts with %s, %s", p.Items[0].Name, p.Items[1].Name)
	}
	for i := 1; i < len(p.Items); i++ {
		if p.Items[i].Start > p.Items[i-1].Start {
			t.Fatalf("item %d starts after item %d", i, i-1)
		}
	}
	if !strings.Contains(h.queries[1].sql, "-extract(epoch FROM os)") || h.queries[1].args[1] != true {
		t.Errorf("past statement or arg is wrong: %v", h.queries[1].args)
	}
}

func TestEventsFilters(t *testing.T) {
	h := &eventsHost{rows: seedEvents()}
	h.install(t)
	cases := []struct {
		name  string
		query map[string][]string
		total int
	}{
		{"search", map[string][]string{"q": {"event 004"}}, 10},
		{"venue", map[string][]string{"venue": {"barn"}}, 270},
		{"category", map[string][]string{"category": {"music"}}, 450},
		{"no venue", map[string][]string{"novenue": {"1"}}, 90},
		{"venue and category", map[string][]string{"venue": {"Anchor"}, "category": {"Talks"}}, 150},
		{"day", map[string][]string{"day": {"2027-03-02"}}, 12},
	}
	for _, c := range cases {
		_, p := getEvents(t, owner, c.query)
		if p.Total != c.total {
			t.Errorf("%s: total %d, want %d", c.name, p.Total, c.total)
		}
		if p.Counts.Upcoming != 900 {
			t.Errorf("%s: the tab count follows the filter: %d", c.name, p.Counts.Upcoming)
		}
	}
	_, p := getEvents(t, owner, map[string][]string{"novenue": {"1"}})
	for _, it := range p.Items {
		if !it.NoVenue || it.Venue != "" {
			t.Fatalf("item with a venue: %+v", it)
		}
	}
	// the search text is escaped for ILIKE and the bridge's JSON text is unwrapped
	h.queries = nil
	getEvents(t, owner, map[string][]string{"q": {`50%_\`}, "venue": {`["Anchor"]`}})
	if a := h.queries[1].args; a[2] != `50\%\_\\` || a[4] != "Anchor" {
		t.Errorf("args = %v", a)
	}
}

func TestEventsBadRequest(t *testing.T) {
	h := &eventsHost{rows: seedEvents()}
	h.install(t)
	for name, q := range map[string]map[string][]string{
		"unknown tab":   {"tab": {"x"}},
		"page zero":     {"page": {"0"}},
		"page too big":  {"page": {"2001"}},
		"page text":     {"page": {"two"}},
		"month":         {"month": {"2026-13"}},
		"long search":   {"q": {strings.Repeat("a", 201)}},
		"day":           {"day": {"2027-02-30"}},
		"day shape":     {"day": {"tomorrow"}},
		"long venue":    {"venue": {strings.Repeat("a", 201)}},
		"long category": {"category": {strings.Repeat("a", 201)}},
	} {
		r, _ := getEvents(t, owner, q)
		if r.Status != 400 || r.Body != `{"error":"bad request"}` {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if _, p := getEvents(t, owner, map[string][]string{"page": {"2000"}, "q": {strings.Repeat("a", 200)}}); len(p.Items) != 0 {
		t.Errorf("limits are inclusive: %+v", p)
	}
	if len(h.queries) != 2 {
		t.Errorf("a bad request ran %d statements", len(h.queries))
	}
}

func TestEventsRolesAndFailures(t *testing.T) {
	h := &eventsHost{rows: seedEvents()}
	h.install(t)
	if r, p := getEvents(t, editor, nil); r.Status != 200 || p.Role != "editor" {
		t.Errorf("editor: %d role %q", r.Status, p.Role)
	}
	if r, _ := getEvents(t, nil, nil); r.Status != 403 {
		t.Errorf("anonymous: %d", r.Status)
	}
	h.notReady = true
	r, p := getEvents(t, owner, nil)
	if r.Status != 200 || p.Ready || len(p.Items) != 0 || p.Total != 0 {
		t.Errorf("before the first reconcile: %d %+v", r.Status, p)
	}
	h.notReady = false
	h.dbErr = errors.New("host said: connection refused to 10.0.0.1")
	r, _ = getEvents(t, owner, nil)
	if r.Status != 503 || strings.Contains(r.Body, "10.0.0.1") {
		t.Errorf("db error: %d %s", r.Status, r.Body)
	}
}

func TestEventsFacets(t *testing.T) {
	h := &eventsHost{rows: seedEvents()}
	h.install(t)
	_, p := getEvents(t, owner, nil)
	if p.Facets != nil {
		t.Errorf("facets sent without a request: %+v", p.Facets)
	}
	_, p = getEvents(t, owner, map[string][]string{"facets": {"1"}})
	if p.Facets == nil || len(p.Facets.Venues) != 3 || len(p.Facets.Categories) != 2 {
		t.Fatalf("facets = %+v", p.Facets)
	}
	last := h.queries[len(h.queries)-1]
	if !strings.Contains(last.sql, "LIMIT $1") || last.args[0] != maxFacet {
		t.Errorf("facet limit: %v", last.args)
	}
}
