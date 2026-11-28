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

	drafts  []map[string]any // drafts of the default type, newest first
	listErr map[string]error // entities.list error by type
	listed  []map[string]any // entities.list arguments, in call order
	onList  func()
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
		case sdk.FnEntitiesList:
			if h.onList != nil {
				h.onList()
			}
			return h.listDrafts(a, out)
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

// listDrafts answers entities.list for drafts: newest first, by cursor, with a
// search on the name.
func (h *eventsHost) listDrafts(a map[string]any, out any) error {
	h.listed = append(h.listed, a)
	schema, _ := a["schema_slug"].(string)
	if err := h.listErr[schema]; err != nil {
		return err
	}
	var matches []map[string]any
	if schema == defaultNewSlug && a["status"] == "draft" {
		for _, d := range h.drafts {
			if q, _ := a["search"].(string); q == "" || strings.Contains(strings.ToLower(d["name"].(string)), strings.ToLower(q)) {
				matches = append(matches, d)
			}
		}
	}
	from := 0
	if c, _ := a["cursor"].(string); c != "" {
		fmt.Sscanf(c, "c%d", &from)
	}
	end := min(from+a["limit"].(int), len(matches))
	next := ""
	if end < len(matches) {
		next = fmt.Sprintf("c%d", end)
	}
	items := []map[string]any{}
	if from < len(matches) {
		items = matches[from:end]
	}
	b, _ := json.Marshal(map[string]any{"items": items, "next_cursor": next})
	return json.Unmarshal(b, out)
}

// seedDrafts makes n drafts, newest first as the host lists them: draft i
// starts i days after 1 April 2027, every 5th has no venue and every 7th is
// an imported one.
func seedDrafts(n int) []map[string]any {
	var out []map[string]any
	for i := n - 1; i >= 0; i-- {
		attrs := map[string]any{"start_at": time.Date(2027, 4, 1, 18, 0, 0, 0, time.UTC).AddDate(0, 0, i).Format(time.RFC3339)}
		if i%5 != 0 {
			attrs["venue_name"] = "Anchor"
		}
		if i%7 == 0 {
			attrs["source_kind"], attrs["source_uid"] = "ical", fmt.Sprintf("ical-feed-%016x", i)
		}
		out = append(out, map[string]any{
			"id": fmt.Sprintf("d%04d", i), "schema_slug": defaultNewSlug, "slug": fmt.Sprintf("d%04d", i),
			"name": fmt.Sprintf("Draft %04d", i), "status": "draft", "attributes": attrs,
		})
	}
	return out
}

func (h *eventsHost) cell(t *testing.T, c sqlCall) string {
	switch {
	case strings.Contains(c.sql, "'truncated'"):
		if h.notReady {
			return `null`
		}
		return h.month(t, c)
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

// month answers eventsMonthSQL: args are from, to, q, category, venue,
// novenue, tz, limit.
func (h *eventsHost) month(t *testing.T, c sqlCall) string {
	from, err := time.Parse(time.RFC3339, c.args[0].(string))
	if err != nil {
		t.Fatal(err)
	}
	to, _ := time.Parse(time.RFC3339, c.args[1].(string))
	q, _ := c.args[2].(string)
	cat, _ := c.args[3].(string)
	venue, _ := c.args[4].(string)
	novenue, _ := c.args[5].(bool)
	loc, err := time.LoadLocation(c.args[6].(string))
	if err != nil {
		t.Fatal(err)
	}
	limit := c.args[7].(int)
	var f []seedEvent
	for _, r := range h.rows {
		switch {
		case !r.Start.Before(to) || !r.End.After(from):
		case q != "" && !strings.Contains(strings.ToLower(r.Name+" "+r.Venue), strings.ToLower(q)):
		case cat != "" && !strings.EqualFold(r.Category, cat):
		case venue != "" && !strings.EqualFold(r.Venue, venue):
		case novenue && r.Venue != "":
		default:
			f = append(f, r)
		}
	}
	sort.SliceStable(f, func(i, j int) bool { return f[i].Start.Before(f[j].Start) })
	days := map[string]int{}
	for _, r := range f {
		days[r.Start.In(loc).Format("2006-01-02")]++
	}
	items := []map[string]any{}
	for _, r := range f[:min(limit, len(f))] {
		items = append(items, map[string]any{"id": r.ID, "name": r.Name, "os": r.Start.Format(time.RFC3339), "all_day": r.AllDay})
	}
	b, _ := json.Marshal(map[string]any{"total": len(f), "truncated": len(f) > limit, "items": items, "day_counts": days})
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

// busyMarch seeds 1700 occurrences in March 2027 (site time), three of them
// all-day, and one that started in February and runs into March.
func busyMarch() []seedEvent {
	var rows []seedEvent
	base := time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	for i := 0; i < 1700; i++ {
		e := seedEvent{ID: fmt.Sprintf("m%04d", i), Name: fmt.Sprintf("March %04d", i), Category: "Music"}
		e.Start = base.Add(time.Duration(i) * 25 * time.Minute)
		e.End = e.Start.Add(time.Hour)
		if i%3 == 0 {
			e.Venue, e.VenueID = "Anchor", "vAnchor"
			e.Category = "Talks"
		}
		rows = append(rows, e)
	}
	rows = append(rows,
		seedEvent{ID: "feb", Name: "From February", Start: time.Date(2027, 2, 27, 9, 0, 0, 0, time.UTC), End: time.Date(2027, 3, 2, 9, 0, 0, 0, time.UTC)},
		seedEvent{ID: "apr", Name: "In April", Start: time.Date(2027, 4, 1, 9, 0, 0, 0, time.UTC), End: time.Date(2027, 4, 1, 10, 0, 0, 0, time.UTC)},
		seedEvent{ID: "allday", Name: "All day", AllDay: true, Start: time.Date(2027, 3, 9, 23, 0, 0, 0, time.UTC), End: time.Date(2027, 3, 10, 23, 0, 0, 0, time.UTC)})
	return rows
}

func TestEventsMonth(t *testing.T) {
	h := &eventsHost{rows: busyMarch()}
	h.install(t)
	r := callRoute(t, "GET", "/admin/events", owner, nil, map[string][]string{"month": {"2027-03"}})
	if r.Status != 200 {
		t.Fatalf("status %d: %s", r.Status, r.Body)
	}
	var p monthPage
	if err := json.Unmarshal([]byte(r.Body), &p); err != nil {
		t.Fatal(err)
	}
	if !p.Ready || p.Month != "2027-03" || p.Page != 1 || p.PerPage != 1500 {
		t.Errorf("envelope = %+v", p)
	}
	if len(p.Items) != 1500 || p.Total != 1702 || !p.Truncated {
		t.Errorf("items %d, total %d, truncated %v", len(p.Items), p.Total, p.Truncated)
	}
	sum := 0
	for _, n := range p.DayCounts {
		sum += n
	}
	if sum != p.Total {
		t.Errorf("day counts add up to %d, total is %d", sum, p.Total)
	}
	if p.DayCounts["2027-02-27"] != 1 || p.DayCounts["2027-04-01"] != 0 {
		t.Errorf("a month event counts on its start day, an April one is out: %v", p.DayCounts)
	}
	if it := p.Items[0]; it.Name != "From February" || it.Day != "2027-02-27" {
		t.Errorf("first = %+v", it)
	}
	if it := p.Items[1]; it.Name != "March 0000" || it.Day != "2027-03-01" || it.Time != "01:00" || it.AllDay {
		t.Errorf("second = %+v", it)
	}
	if p.Counts.Upcoming == 0 {
		t.Errorf("counts are missing: %+v", p.Counts)
	}
	sql, a := h.queries[1].sql, h.queries[1].args
	// 1 March 00:00 in Berlin is 23:00 UTC the day before
	if a[0] != "2027-02-28T23:00:00Z" || a[1] != "2027-03-31T22:00:00Z" || a[6] != "Europe/Berlin" || a[7] != 1500 {
		t.Errorf("args = %v", a)
	}
	if !strings.Contains(sql, "LIMIT $8") || !strings.Contains(sql, "AT TIME ZONE $7::text") || strings.Contains(sql, "o.day") {
		t.Errorf("month statement: %s", sql)
	}
}

func TestEventsMonthSmallAndFiltered(t *testing.T) {
	h := &eventsHost{rows: busyMarch()}
	h.install(t)
	get := func(q map[string][]string) monthPage {
		t.Helper()
		q["month"] = []string{"2027-03"}
		r := callRoute(t, "GET", "/admin/events", editor, nil, q)
		var p monthPage
		if r.Status != 200 || json.Unmarshal([]byte(r.Body), &p) != nil {
			t.Fatalf("status %d: %s", r.Status, r.Body)
		}
		return p
	}
	p := get(map[string][]string{"novenue": {"1"}})
	if p.Total != 1700-567+2 || p.Truncated || len(p.Items) != p.Total || p.Role != "editor" {
		t.Errorf("no venue: total %d items %d truncated %v", p.Total, len(p.Items), p.Truncated)
	}
	if p := get(map[string][]string{"category": {"talks"}, "venue": {"anchor"}}); p.Total != 567 {
		t.Errorf("category and venue: total %d", p.Total)
	}
	if p := get(map[string][]string{"q": {"all day"}, "tab": {"past"}}); p.Total != 1 || p.Items[0].Time != "" || !p.Items[0].AllDay {
		t.Errorf("all-day: %+v", p)
	}
	if p := get(map[string][]string{"facets": {"1"}}); p.Facets == nil || len(p.Facets.Venues) != 3 {
		t.Errorf("facets = %+v", p.Facets)
	}
	h.notReady = true
	if p := get(map[string][]string{}); p.Ready || len(p.Items) != 0 {
		t.Errorf("before the first reconcile: %+v", p)
	}
}

func TestEventsMonthBadRequest(t *testing.T) {
	h := &eventsHost{rows: busyMarch()}
	h.install(t)
	for name, q := range map[string]map[string][]string{
		"drafts":    {"month": {"2027-03"}, "tab": {"drafts"}},
		"page two":  {"month": {"2027-03"}, "page": {"2"}},
		"month 13":  {"month": {"2026-13"}},
		"month 00":  {"month": {"2027-00"}},
		"year 1999": {"month": {"1999-12"}},
		"no zero":   {"month": {"2027-3"}},
	} {
		r := callRoute(t, "GET", "/admin/events", owner, nil, q)
		if r.Status != 400 || r.Body != `{"error":"bad request"}` {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
	}
	if len(h.queries) != 0 {
		t.Errorf("a bad request ran %d statements", len(h.queries))
	}
}

func TestEventsDraftsTab(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(120)}
	h.install(t)
	r, p1 := getEvents(t, owner, map[string][]string{"tab": {"drafts"}})
	if r.Status != 200 || !p1.Ready || p1.Total != 120 || p1.TotalCapped || len(p1.Items) != 50 {
		t.Fatalf("page 1: %d ready %v total %d items %d", r.Status, p1.Ready, p1.Total, len(p1.Items))
	}
	// soonest first although the host lists newest first
	if p1.Items[0].Name != "Draft 0000" || p1.Items[0].Status != "draft" || p1.Items[0].Day != "2027-04-01" || p1.Items[0].Time != "20:00" {
		t.Errorf("first = %+v", p1.Items[0])
	}
	if p1.Counts.Drafts == nil || *p1.Counts.Drafts != 120 || p1.Counts.DraftsCapped {
		t.Errorf("counts = %+v", p1.Counts)
	}
	if !p1.Items[7].Imported || p1.Items[8].Imported || !p1.Items[0].NoVenue || p1.Items[1].NoVenue {
		t.Errorf("flags: %+v %+v", p1.Items[7], p1.Items[0])
	}
	_, p2 := getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "page": {"2"}})
	if len(p2.Items) != 50 || p2.Items[0].Name != "Draft 0050" {
		t.Errorf("page 2 starts at %s", p2.Items[0].Name)
	}
	_, p3 := getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "page": {"3"}})
	if len(p3.Items) != 20 {
		t.Errorf("page 3 has %d", len(p3.Items))
	}
	_, p9 := getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "page": {"9"}})
	if len(p9.Items) != 0 || p9.Total != 120 {
		t.Errorf("a page past the end: %d items, total %d", len(p9.Items), p9.Total)
	}
	// no index statement for the drafts themselves
	for _, q := range h.queries {
		if q.sql != eventCountsSQL {
			t.Fatalf("unexpected statement: %s", q.sql)
		}
	}
}

func TestEventsDraftsFilters(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(120)}
	h.install(t)
	_, p := getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "novenue": {"1"}})
	if p.Total != 24 {
		t.Errorf("no venue: total %d", p.Total)
	}
	for _, it := range p.Items {
		if !it.NoVenue {
			t.Fatalf("item with a venue: %+v", it)
		}
	}
	_, p = getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "q": {"draft 003"}, "venue": {"Nowhere"}, "category": {"x"}})
	if p.Total != 10 || h.listed[len(h.listed)-2]["search"] != "draft 003" {
		t.Errorf("search: total %d", p.Total)
	}
	_, p = getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "day": {"2027-04-03"}})
	if p.Total != 1 || p.Items[0].Name != "Draft 0002" {
		t.Errorf("day: %+v", p)
	}
}

func TestEventsDraftsCapped(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(650)}
	h.install(t)
	_, p := getEvents(t, owner, map[string][]string{"tab": {"drafts"}, "page": {"10"}})
	if p.Total != 500 || !p.TotalCapped || len(p.Items) != 50 || p.Counts.Drafts == nil || *p.Counts.Drafts != 500 || !p.Counts.DraftsCapped {
		t.Errorf("total %d capped %v items %d counts %+v", p.Total, p.TotalCapped, len(p.Items), p.Counts)
	}
	h.listErr = map[string]error{defaultNewSlug: errors.New("host said no")}
	if r, _ := getEvents(t, owner, map[string][]string{"tab": {"drafts"}}); r.Status != 503 {
		t.Errorf("scan error: %d", r.Status)
	}
}

func TestEventsDraftCountOnOtherTabs(t *testing.T) {
	h := &eventsHost{rows: seedEvents(), drafts: seedDrafts(7)}
	h.install(t)
	r, p := getEvents(t, owner, nil)
	if p.Counts.Drafts != nil || strings.Contains(r.Body, `"drafts"`) || len(h.listed) != 0 {
		t.Errorf("the drafts were counted unasked: %s", r.Body)
	}
	_, p = getEvents(t, owner, map[string][]string{"with_drafts": {"1"}})
	if p.Counts.Drafts == nil || *p.Counts.Drafts != 7 || p.Counts.DraftsCapped || p.Total != 900 {
		t.Errorf("counts = %+v", p.Counts)
	}
	h.listed = nil
	h.rows = busyMarch()
	r2 := callRoute(t, "GET", "/admin/events", owner, nil, map[string][]string{"month": {"2027-03"}, "with_drafts": {"1"}})
	var m monthPage
	if err := json.Unmarshal([]byte(r2.Body), &m); err != nil || m.Counts.Drafts == nil || *m.Counts.Drafts != 7 {
		t.Errorf("month counts = %+v (%v)", m.Counts, err)
	}
	// a failing scan leaves the count out and the page still answers
	h.listErr = map[string]error{defaultNewSlug: errors.New("host said no")}
	r, p = getEvents(t, owner, map[string][]string{"with_drafts": {"1"}})
	if r.Status != 200 || p.Counts.Drafts != nil {
		t.Errorf("failing scan: %d %+v", r.Status, p.Counts)
	}
}
