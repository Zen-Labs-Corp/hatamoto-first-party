package main

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const (
	gcalURL  = "https://calendar.google.com/calendar/ical/example%40example.org/public/basic.ics"
	meetURL  = "https://www.meetup.com/example-group/events/ical/"
	otherURL = "https://example.org/unlisted.ics"
)

func feedOf(n int, prefix string) string {
	var evs []string
	for i := range n {
		evs = append(evs, vevent(fmt.Sprintf("UID:%s%d@example.org", prefix, i), fmt.Sprintf("SUMMARY:Event %s%d", prefix, i), "DTSTART:20270612T100000Z"))
	}
	return icsBody(evs...)
}

// runFetch drives the fetch steps the way the host does: each step starts from
// the last checkpoint.
func (h *runHost) runFetch(t *testing.T) (steps int) {
	t.Helper()
	h.phase = "fetch"
	for steps < 30 {
		steps++
		h.finished, h.failed = nil, ""
		if _, err := onStep(sdk.RunStep{RunID: h.runID, Phase: sdk.RunPhaseFetch, Cursor: h.cursor}); err != nil {
			t.Fatalf("step error: %v", err)
		}
		if h.finished != nil || h.failed != "" {
			return steps
		}
	}
	t.Fatal("the fetch phase did not end")
	return steps
}

func (h *runHost) records(t *testing.T) []importRecord {
	t.Helper()
	var keys []string
	for k := range h.buffer {
		if strings.HasPrefix(k, bufEvent) {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	var out []importRecord
	for _, k := range keys {
		var r importRecord
		if err := json.Unmarshal(h.buffer[k], &r); err != nil {
			t.Fatal(err)
		}
		out = append(out, r)
	}
	return out
}

func sourcesSetting(entries ...string) string {
	return `{"import_sources":[` + strings.Join(entries, ",") + `]}`
}

func src(name, kind, url string, extra ...string) string {
	return fmt.Sprintf(`{"name":%q,"kind":%q,"url":%q%s}`, name, kind, url, strings.Join(extra, ""))
}

func TestImportKey(t *testing.T) {
	a := importKey("google", "Town Hall: Events & More, long name here", "u1")
	if a != importKey("google", "Town Hall: Events & More, long name here", "u1") || a == importKey("google", "Town Hall: Events & More, long name here", "u2") {
		t.Error("the key must follow the UID")
	}
	if !strings.HasPrefix(a, "google-town-hall-events-more-lo-") || strings.HasPrefix(a, keyPrefix) {
		t.Errorf("key %q", a)
	}
	if k := importKey("ical", "dup-", "x"); strings.HasPrefix(k, keyPrefix) {
		t.Errorf("key %q starts with %q", k, keyPrefix)
	}
	if k := importKey("ical", "", "x"); !strings.HasPrefix(k, "ical--") || len(k) != len("ical--")+16 {
		t.Errorf("key %q", k)
	}
}

func TestLoadImportSources(t *testing.T) {
	var entries []string
	entries = append(entries,
		src("Google", "google", gcalURL, `,"category":"Music"`),
		src("Off", "google", gcalURL, `,"enabled":false`),
		src("Unlisted host", "google", otherURL),
		src("Wrong kind", "eventbrite", "https://www.eventbrite.com/o/example-1"),
		src("Plain http", "outlook", "http://outlook.office365.com/x.ics"),
		src("Webcal", "outlook", "webcal://outlook.office365.com/owa/calendar/x/calendar.ics"),
		src("", "meetup", meetURL),
	)
	for i := range 10 {
		entries = append(entries, src(fmt.Sprintf("More %d", i), "meetup", meetURL))
	}
	h := &runHost{settings: sourcesSetting(entries...)}
	h.install(t)
	got := loadImportSources()
	if len(got) != maxSources {
		t.Fatalf("%d sources, want the cap %d: %+v", len(got), maxSources, got)
	}
	want := []importSource{
		{"Google", "google", gcalURL, "Music"},
		{"Webcal", "outlook", "https://outlook.office365.com/owa/calendar/x/calendar.ics", ""},
		{"www.meetup.com", "meetup", meetURL, ""},
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("source %d = %+v, want %+v", i, got[i], w)
		}
	}
}

func TestImportCaps(t *testing.T) {
	t.Run("101st event is dropped", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1", settings: sourcesSetting(src("Google", "google", gcalURL), src("Meetup", "meetup", meetURL)),
			pages: map[string]fakePage{gcalURL: {status: 200, body: feedOf(60, "g")}, meetURL: {status: 200, body: feedOf(60, "m")}}}
		h.install(t)
		if steps := h.runFetch(t); steps != 2 {
			t.Errorf("%d steps, one source per step wanted", steps)
		}
		if h.failed != "" || h.finished == nil {
			t.Fatalf("failed %q finished %v", h.failed, h.finished)
		}
		if got := len(h.records(t)); got != maxImportEvents {
			t.Errorf("%d events buffered, want %d", got, maxImportEvents)
		}
		if string(h.buffer[bufStat]) != `{"over":20}` {
			t.Errorf("stat %s", h.buffer[bufStat])
		}
	})

	t.Run("truncated answer is a source error", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1", settings: sourcesSetting(src("Google", "google", gcalURL), src("Meetup", "meetup", meetURL)),
			pages: map[string]fakePage{gcalURL: {status: 200, body: feedOf(3, "g"), truncated: true}, meetURL: {status: 200, body: feedOf(2, "m")}}}
		h.install(t)
		h.runFetch(t)
		recs := h.records(t)
		if h.failed != "" || len(recs) != 2 || recs[0].Source != "Meetup" {
			t.Fatalf("failed %q records %+v", h.failed, recs)
		}
		note := string(h.buffer["err/00"])
		if !strings.Contains(note, "too large") || strings.Contains(note, "calendar.google.com") || strings.Contains(note, "example") {
			t.Errorf("error note %s", note)
		}
	})

	t.Run("a blocked host is a source error and the others still import", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1", settings: sourcesSetting(src("Google", "google", gcalURL), src("Meetup", "meetup", meetURL)),
			pages: map[string]fakePage{gcalURL: {err: &sdk.HostError{Code: sdk.ErrBlocked}}, meetURL: {status: 200, body: feedOf(2, "m")}}}
		h.install(t)
		h.runFetch(t)
		if h.failed != "" || len(h.records(t)) != 2 || h.buffer["err/00"] == nil {
			t.Errorf("failed %q records %d buffer %v", h.failed, len(h.records(t)), h.buffer)
		}
	})

	t.Run("every source failing fails the run without the address", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1", settings: sourcesSetting(src("Google", "google", gcalURL)),
			pages: map[string]fakePage{gcalURL: {status: 404}}}
		h.install(t)
		h.runFetch(t)
		if !strings.HasPrefix(h.failed, "No calendar could be read") || strings.Contains(h.failed, "google.com") {
			t.Errorf("failed %q", h.failed)
		}
	})

	t.Run("duplicate UIDs are imported once", func(t *testing.T) {
		twice := icsBody(
			vevent("UID:same@example.org", "SUMMARY:First", "DTSTART:20270612T100000Z"),
			vevent("UID:same@example.org", "SUMMARY:Second", "DTSTART:20270612T100000Z"),
			vevent("UID:other@example.org", "SUMMARY:Other", "DTSTART:20270613T100000Z"))
		h := &runHost{name: runImportScheduled, runID: "r1", settings: sourcesSetting(src("Google", "google", gcalURL)),
			pages: map[string]fakePage{gcalURL: {status: 200, body: twice}}}
		h.install(t)
		h.runFetch(t)
		recs := h.records(t)
		if len(recs) != 2 {
			t.Fatalf("records %+v", recs)
		}
		titles := []string{recs[0].Ev.Title, recs[1].Ev.Title}
		sort.Strings(titles)
		if titles[0] != "First" || titles[1] != "Other" {
			t.Errorf("titles %v", titles)
		}
		for _, r := range recs {
			if r.Kind != "google" || r.Source != "Google" || r.Key != importKey("google", "Google", r.Ev.UID) || strings.HasPrefix(r.Key, keyPrefix) {
				t.Errorf("record %+v", r)
			}
		}
	})

	t.Run("disabled and unlisted sources are not fetched", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1",
			settings: sourcesSetting(src("Off", "google", gcalURL, `,"enabled":false`), src("Unlisted", "google", otherURL), src("Meetup", "meetup", meetURL)),
			pages:    map[string]fakePage{meetURL: {status: 200, body: feedOf(1, "m")}}}
		h.install(t)
		h.runFetch(t)
		if len(h.fetched) != 1 || h.fetched[0] != meetURL {
			t.Errorf("fetched %v", h.fetched)
		}
	})

	t.Run("no sources finishes quietly", func(t *testing.T) {
		h := &runHost{name: runImportScheduled, runID: "r1", settings: `{}`}
		h.install(t)
		h.runFetch(t)
		if h.failed != "" || h.finished == nil || len(h.fetched) != 0 {
			t.Errorf("failed %q finished %v fetched %v", h.failed, h.finished, h.fetched)
		}
	})
}

func TestImportURLFetch(t *testing.T) {
	page := `<html><script type="application/ld+json">{"@type":"Event","name":"Jam","startDate":"2027-06-12T19:00:00Z","url":"https://example.org/e/1"}</script></html>`
	h := &runHost{name: runEditDates, runID: "r2", params: map[string]any{"action": "import_url", "url": "webcal://example.org/one.ics", "kind": "eventbrite", "name": "One"},
		pages: map[string]fakePage{"https://example.org/one.ics": {status: 200, body: page}}}
	h.install(t)
	h.runFetch(t)
	recs := h.records(t)
	if h.failed != "" || len(recs) != 1 || recs[0].Kind != "eventbrite" || recs[0].Source != "One" || recs[0].Ev.Title != "Jam" {
		t.Fatalf("failed %q records %+v", h.failed, recs)
	}

	for name, params := range map[string]map[string]any{
		"no address": {"action": "import_url"},
		"bad scheme": {"action": "import_url", "url": "ftp://example.org/x"},
		"bad kind":   {"action": "import_url", "url": "https://example.org/x", "kind": "outlook2"},
	} {
		h := &runHost{name: runEditDates, runID: "r3", params: params}
		h.install(t)
		h.runFetch(t)
		if h.failed == "" {
			t.Errorf("%s: the run should fail", name)
		}
	}

	h = &runHost{name: runEditDates, runID: "r4", params: map[string]any{"action": "import_url", "url": "https://example.org/page"},
		pages: map[string]fakePage{"https://example.org/page": {status: 200, body: "<html>nothing here</html>"}}}
	h.install(t)
	h.runFetch(t)
	if !strings.HasPrefix(h.failed, "No calendar could be read") {
		t.Errorf("failed %q", h.failed)
	}
}
