package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func TestImportMark(t *testing.T) {
	at := time.Date(2027, 6, 12, 10, 0, 0, 0, time.UTC)
	m := importMark{Start: at, Key: "ical-club-0000000000000005"}
	if got := m.String(); got != "2027-06-12T10:00:00Z|ical-club-0000000000000005" {
		t.Errorf("String = %q", got)
	}
	if got := (importMark{}).String(); got != "" {
		t.Errorf("no mark = %q", got)
	}
	for name, c := range map[string]struct {
		start time.Time
		key   string
		want  bool
	}{
		"earlier start":      {at.Add(-time.Minute), "z", true},
		"same start, lower":  {at, "ical-club-0000000000000004", true},
		"same start, equal":  {at, m.Key, true},
		"same start, higher": {at, "ical-club-0000000000000006", false},
		"later start":        {at.Add(time.Minute), "a", false},
	} {
		if got := m.covers(c.start, c.key); got != c.want {
			t.Errorf("%s: covers = %v", name, got)
		}
	}
	// the zone of the start does not matter
	berlin := time.FixedZone("CEST", 2*3600)
	if !m.covers(at.In(berlin), m.Key) || m.later(importMark{Start: at.In(berlin), Key: m.Key}) {
		t.Error("the same instant in another zone is the same place")
	}
}

func TestSourceStatsTakeTheLatestOfIndexAndDrafts(t *testing.T) {
	prefix := importPrefix("ical", "Club")
	at := time.Date(2027, 6, 12, 10, 0, 0, 0, time.UTC)
	newStats := func() sourceStats {
		return sourceStats{Imported: 5, CursorStart: &at, CursorKey: prefix + "0000000000000005"}
	}
	draft := func(kind, uid string, start time.Time, has bool) draftRow {
		return draftRow{SourceKind: kind, SourceUID: uid, Start: start, HasStart: has}
	}
	for name, c := range map[string]struct {
		drafts   []draftRow
		imported int
		mark     string
	}{
		"no drafts":                    {nil, 5, "2027-06-12T10:00:00Z|" + prefix + "0000000000000005"},
		"a later draft":                {[]draftRow{draft("ical", prefix+"0000000000000001", at.Add(time.Hour), true)}, 6, "2027-06-12T11:00:00Z|" + prefix + "0000000000000001"},
		"the same start, a higher key": {[]draftRow{draft("ical", prefix+"0000000000000009", at, true)}, 6, "2027-06-12T10:00:00Z|" + prefix + "0000000000000009"},
		"an earlier draft":             {[]draftRow{draft("ical", prefix+"0000000000000099", at.Add(-time.Hour), true)}, 6, "2027-06-12T10:00:00Z|" + prefix + "0000000000000005"},
		"another source":               {[]draftRow{draft("ical", importPrefix("ical", "Other")+"01", at.Add(time.Hour), true)}, 5, "2027-06-12T10:00:00Z|" + prefix + "0000000000000005"},
		"another kind":                 {[]draftRow{draft("google", prefix+"01", at.Add(time.Hour), true)}, 5, "2027-06-12T10:00:00Z|" + prefix + "0000000000000005"},
		"a draft without a start":      {[]draftRow{draft("ical", prefix+"0000000000000077", time.Time{}, false)}, 6, "2027-06-12T10:00:00Z|" + prefix + "0000000000000005"},
	} {
		st := newStats()
		st.withDrafts("ical", prefix, c.drafts)
		if st.Imported != c.imported || st.mark().String() != c.mark {
			t.Errorf("%s: imported %d, mark %s", name, st.Imported, st.mark())
		}
	}
	// a source with only drafts has a mark from them
	var none sourceStats
	none.withDrafts("ical", prefix, []draftRow{draft("ical", prefix+"0000000000000001", at, true), draft("ical", prefix+"0000000000000002", at, true)})
	if none.Imported != 2 || none.mark().Key != prefix+"0000000000000002" {
		t.Errorf("drafts only: %+v", none)
	}
	// and a source with nothing has none
	var empty sourceStats
	empty.withDrafts("ical", prefix, nil)
	if empty.mark().String() != "" || empty.CursorStart != nil {
		t.Errorf("empty: %+v", empty)
	}
}

const clubSettings = `{"import_sources":[{"name":"Club","kind":"ical","url":"https://events.example.org/feed.ics","schedule":"daily"}]}`

func TestAdminImportsProgress(t *testing.T) {
	prefix := importPrefix("ical", "Club")
	const stats = `{"last":"2027-02-01T10:00:00+00:00","last_new":null,"today":0,"imported":150,"cursor_start":"2027-06-12T10:00:00+00:00","cursor_key":"ical-club-0000000000000005"}`
	draft := `[{"id":"d1","schema_slug":"calendar-event","slug":"d1","name":"Later draft","status":"draft","attributes":{"start_at":"2027-06-13T10:00:00Z","source_kind":"ical","source_uid":"` + prefix + `0000000000000001"}},
{"id":"d2","schema_slug":"calendar-event","slug":"d2","name":"Own draft","status":"draft","attributes":{"start_at":"2027-06-14T10:00:00Z"}}]`
	h := &adminHost{settings: clubSettings, last: stats, drafts: draft,
		progress: `{"` + prefix + `":{"found":214,"found_capped":false,"remaining":14,"counted_at":"2027-02-01T10:00:00+00:00"}}`}
	h.install(t)
	r := callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	var got importList
	if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil || len(got.Sources) != 1 || got.BatchCap != 200 {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	row := got.Sources[0]
	if row.Imported != 151 || row.Cursor != "2027-06-13T10:00:00Z|"+prefix+"0000000000000001" {
		t.Errorf("imported %d, cursor %q", row.Imported, row.Cursor)
	}
	if row.Found == nil || *row.Found != 214 || row.Remaining == nil || *row.Remaining != 14 || row.CountedAt == nil || row.Finished || row.FoundCapped {
		t.Errorf("count = %+v", row)
	}
	// removed sources are pruned first, then the row is stored with the drafts' cursor
	if len(h.execs) != 2 || !strings.HasPrefix(h.execs[0].sql, "DELETE FROM import_progress") || h.execs[0].args[0] != `["`+prefix+`"]` {
		t.Fatalf("execs = %+v", h.execs)
	}
	up := h.execs[1]
	if !strings.Contains(up.sql, "ON CONFLICT (source_key) DO UPDATE") || up.args[0] != prefix || up.args[1] != 151 ||
		up.args[2] != "2027-06-13T10:00:00Z" || up.args[3] != prefix+"0000000000000001" {
		t.Errorf("upsert = %+v", up)
	}
	for _, e := range h.execs {
		if strings.Contains(e.sql, "Club") || strings.Contains(e.sql, "feed.ics") {
			t.Errorf("a value in the statement text: %s", e.sql)
		}
	}
	// a finished source
	h.progress = `{"` + prefix + `":{"found":151,"remaining":0}}`
	r = callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if !strings.Contains(r.Body, `"finished":true`) {
		t.Errorf("not finished: %s", r.Body)
	}
	// events before the cursor that were never created: not finished
	h.progress = `{"` + prefix + `":{"found":214,"remaining":0}}`
	r = callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if !strings.Contains(r.Body, `"finished":false`) {
		t.Errorf("events left behind the cursor, finished: %s", r.Body)
	}
	// a capped count (the first page of a feed) never means finished
	h.progress = `{"` + prefix + `":{"found":50,"found_capped":true,"remaining":0}}`
	r = callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if !strings.Contains(r.Body, `"finished":false`) {
		t.Errorf("a capped count is finished: %s", r.Body)
	}
	// no source: every stored row goes
	h.settings, h.execs = `{}`, nil
	callRoute(t, "GET", "/admin/imports", owner, nil, nil)
	if len(h.execs) != 1 || h.execs[0].args[0] != `[]` {
		t.Errorf("execs = %+v", h.execs)
	}
}

func TestAdminImportsCount(t *testing.T) {
	var keys []string
	for i := range 214 {
		keys = append(keys, importKey("ical", "Club", fmt.Sprintf("c%d@example.org", i)))
	}
	sort.Strings(keys) // the feed's events start at the same time: they go by key
	stats := fmt.Sprintf(`{"imported":200,"cursor_start":"2027-06-12T10:00:00+00:00","cursor_key":%q}`, keys[199])
	const feed = "https://events.example.org/feed.ics"
	h := &adminHost{settings: clubSettings, last: stats, fetch: map[string]fakePage{feed: {status: 200, body: feedOf(214, "c")}},
		progress: `{"ical-club-":{"found":214,"remaining":14,"counted_at":"2027-02-01T10:00:00+00:00"}}`}
	h.install(t)
	r := callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {"Club"}})
	var got countAnswer
	if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil || got.CountError != "" {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if got.Source.Name != "Club" || got.Source.Found == nil || *got.Source.Found != 214 || got.Source.Remaining == nil || *got.Source.Remaining != 14 || got.Source.Imported != 200 {
		t.Errorf("source = %+v", got.Source)
	}
	if len(h.fetched) != 1 || h.fetchMs[0] != float64(countFetchMs) && h.fetchMs[0] != countFetchMs {
		t.Errorf("fetches %v, timeouts %v", h.fetched, h.fetchMs)
	}
	if len(h.execs) != 1 || !strings.Contains(h.execs[0].sql, "found_capped") {
		t.Fatalf("execs = %+v", h.execs)
	}
	e := h.execs[0].args
	if e[0] != "ical-club-" || e[1] != 200 || e[3] != keys[199] || e[4] != 214 || e[5] != false || e[6] != 14 {
		t.Errorf("count args = %v", e)
	}
	// without a mark every event is left
	h.last, h.execs = `{"imported":0,"cursor_start":null,"cursor_key":null}`, nil
	callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {"Club"}})
	if e := h.execs[0].args; e[2] != nil || e[3] != "" || e[4] != 214 || e[6] != 214 {
		t.Errorf("no mark args = %v", e)
	}
}

func TestAdminImportsCountFailsOpen(t *testing.T) {
	const feed = "https://events.example.org/feed.ics"
	for name, page := range map[string]fakePage{
		"blocked":    {err: &sdk.HostError{Code: sdk.ErrBlocked, Message: feed}},
		"status":     {status: 500, body: "x"},
		"too large":  {status: 200, truncated: true},
		"not a feed": {status: 200, body: "<html>nothing here</html>"},
	} {
		h := &adminHost{settings: clubSettings, last: `{"imported":3}`, fetch: map[string]fakePage{feed: page}}
		h.install(t)
		r := callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {"Club"}})
		var got countAnswer
		if r.Status != 200 || json.Unmarshal([]byte(r.Body), &got) != nil || got.CountError == "" || got.Source.Imported != 3 || got.Source.Name != "Club" {
			t.Errorf("%s: %d %s", name, r.Status, r.Body)
		}
		if strings.Contains(got.CountError, "example.org") {
			t.Errorf("%s: the reason has the address: %q", name, got.CountError)
		}
		// the progress is kept, the count is not touched
		if len(h.execs) != 1 || strings.Contains(h.execs[0].sql, "found") {
			t.Errorf("%s: execs = %+v", name, h.execs)
		}
	}
}

func TestAdminImportsCountRefusals(t *testing.T) {
	h := &adminHost{settings: `{"import_sources":[{"name":"Off","kind":"ical","url":"https://events.example.org/off.ics","enabled":false}]}`}
	h.install(t)
	for _, name := range []string{"Nope", "Off", "club"} {
		r := callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {name}})
		if r.Status != 404 || r.Body != `{"error":"not found"}` {
			t.Errorf("%q: %d %s", name, r.Status, r.Body)
		}
	}
	if r := callRoute(t, "GET", "/admin/imports", editor, nil, map[string][]string{"count": {"Off"}}); r.Status != 403 {
		t.Errorf("editor: %d", r.Status)
	}
	if len(h.fetched) != 0 || len(h.execs) != 0 {
		t.Errorf("fetched %v, execs %v", h.fetched, h.execs)
	}
	h.dbErr = fmt.Errorf("no db")
	h.settings = clubSettings
	if r := callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {"Club"}}); r.Status != 503 || strings.Contains(r.Body, "no db") {
		t.Errorf("db down: %d %s", r.Status, r.Body)
	}
}

func TestAdminImportsCountEventbriteReadsOnePage(t *testing.T) {
	h := &adminHost{settings: `{"eventbrite_token":"` + ebToken + `","import_sources":[{"name":"Tickets","kind":"eventbrite","organization_id":"` + ebOrg + `"}]}`,
		last: `{"imported":1}`, fetch: map[string]fakePage{ebBase: {status: 200, body: ebPage1}}}
	h.install(t)
	r := callRoute(t, "GET", "/admin/imports", owner, nil, map[string][]string{"count": {"Tickets"}})
	if r.Status != 200 || strings.Contains(r.Body, ebToken) {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if len(h.fetched) != 1 || h.fetched[0] != ebBase {
		t.Errorf("fetched %v", h.fetched)
	}
	if len(h.execs) != 1 {
		t.Fatalf("execs = %+v", h.execs)
	}
	if e := h.execs[0].args; e[4] != 2 || e[5] != true || e[6] != 2 {
		t.Errorf("args = %v", e)
	}
}
