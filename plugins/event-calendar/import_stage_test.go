package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func buffered(t *testing.T, h *runHost, recs ...importRecord) {
	t.Helper()
	if h.buffer == nil {
		h.buffer = map[string][]byte{}
	}
	for _, r := range recs {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		h.buffer[bufEvent+r.Key] = b
	}
}

func record(kind, source, uid string, ev importEvent) importRecord {
	ev.UID = uid
	return importRecord{Source: source, Kind: kind, Key: importKey(kind, source, uid), Ev: ev}
}

func attrText(t *testing.T, m map[string]json.RawMessage, k string) string {
	t.Helper()
	var s string
	_ = json.Unmarshal(m[k], &s)
	return s
}

func existingJSON(rec importRecord, slug, extra string) string {
	return `{"id":"x-` + slug + `","schema_slug":"calendar-event","slug":"` + slug + `","name":"` + rec.Ev.Title + `","locale":"en","status":"published","attributes":{` +
		`"start_at":"2027-06-12T12:00:00+02:00","source_kind":"` + rec.Kind + `","source_uid":"` + rec.Key + `"` + extra + `}}`
}

func TestImportStagesCreatesAndUpdates(t *testing.T) {
	loc := time.UTC
	start := time.Date(2027, 6, 12, 10, 0, 0, 0, loc)
	plain := record("google", "Town", "plain@example.org", importEvent{Title: "Open mic", Start: start, End: start.Add(2 * time.Hour), Venue: "Town Hall", Description: "Bring a friend."})
	text := record("google", "Town", "text@example.org", importEvent{Title: "Street fair", Start: start, Venue: "Unknown Barn", Address: "Main Street 1", City: "Example City"})
	linked := record("meetup", "Group", "link@example.org", importEvent{Title: "Meetup night", Start: start, Website: "https://example.org/e/1"})
	allDay := record("google", "Town", "day@example.org", importEvent{Title: "Fair", Start: time.Date(2027, 7, 1, 0, 0, 0, 0, loc), End: time.Date(2027, 7, 2, 0, 0, 0, 0, loc), AllDay: true, Status: "cancelled",
		Repeat: map[string]any{"repeat": "weekly", "repeat_every": 2, "repeat_weekdays": []string{"tu"}}, Dropped: 0})
	odd := record("google", "Town", "odd@example.org", importEvent{Title: "Odd", Start: start, Dropped: 1})

	h := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r1", created: true, lists: map[string][]string{
		"calendar-venue":    {`{"id":"v1","name":"town hall"}`},
		"calendar-category": {`{"id":"c1","name":"Music"}`},
	}}
	plain.Category = "music"
	h.install(t)
	buffered(t, h, plain, text, linked, allDay, odd)
	h.buffer[bufError+"01"] = []byte(`{"source":"x","reason":"y"}`)
	h.step(t)
	if h.failed != "" || len(h.upserts) != 5 {
		t.Fatalf("failed %q, %d upserts", h.failed, len(h.upserts))
	}
	byKey := map[string]sdk.EntityUpsert{}
	for _, up := range h.upserts {
		byKey[up.SourceKey] = up
		attrs := attrsOf(t, up)
		if up.Schema != "calendar-event" || up.Slug != "" || up.Name == "" {
			t.Errorf("upsert %+v", up)
		}
		if _, bad := attrs["source_url"]; bad || attrText(t, attrs, "source_uid") != up.SourceKey || strings.HasPrefix(up.SourceKey, keyPrefix) {
			t.Errorf("provenance of %s: %s", up.SourceKey, up.Attributes)
		}
		if _, bad := attrs["image"]; bad {
			t.Errorf("image set: %s", up.Attributes)
		}
		for _, k := range []string{"start_at", "end_at"} {
			if v := attrText(t, attrs, k); v != "" {
				if _, err := time.Parse(time.RFC3339, v); err != nil {
					t.Errorf("%s %q is not the editor's form", k, v)
				}
			}
		}
	}
	a := attrsOf(t, byKey[plain.Key])
	if attrText(t, a, "source_kind") != "google" || attrText(t, a, "start_at") != "2027-06-12T10:00:00Z" || attrText(t, a, "end_at") != "2027-06-12T12:00:00Z" ||
		string(a["venue"]) != `["v1"]` || string(a["category"]) != `["c1"]` || a["venue_name"] != nil || attrText(t, a, "description") != "Bring a friend." {
		t.Errorf("plain: %s", byKey[plain.Key].Attributes)
	}
	a = attrsOf(t, byKey[text.Key])
	if a["venue"] != nil || attrText(t, a, "venue_name") != "Unknown Barn" || attrText(t, a, "venue_address") != "Main Street 1" || attrText(t, a, "venue_city") != "Example City" || a["end_at"] != nil {
		t.Errorf("text venue: %s", byKey[text.Key].Attributes)
	}
	a = attrsOf(t, byKey[allDay.Key])
	if string(a["all_day"]) != "true" || attrText(t, a, "event_status") != "cancelled" || string(a["repeat_every"]) != "2" || string(a["repeat_weekdays"]) != `["tu"]` || attrText(t, a, "repeat") != "weekly" {
		t.Errorf("all day: %s", byKey[allDay.Key].Attributes)
	}
	sum, _ := h.finished.(sdk.RunFinishArgs)
	var got map[string]int
	b, _ := json.Marshal(sum.Summary)
	_ = json.Unmarshal(b, &got)
	want := map[string]int{"created": 5, "updated": 0, "unchanged": 0, "with_links": 1, "failed": 0, "failed_sources": 1, "dropped_recurrence": 1}
	for k, v := range want {
		if got[k] != v {
			t.Errorf("summary %s = %d, want %d (%v)", k, got[k], v, got)
		}
	}

	// the second run with the same feed stages nothing, whatever the stored offset is
	h2 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r2", lists: map[string][]string{
		"calendar-event": {
			existingJSON(plain, "open-mic", `,"end_at":"2027-06-12T14:00:00+02:00","description":"Bring a friend.","venue":["v1"],"category":["c1"]`),
			existingJSON(text, "street-fair", `,"venue_name":"Unknown Barn","venue_address":"Main Street 1","venue_city":"Example City"`),
		},
		"calendar-venue":    {`{"id":"v1","name":"Town Hall"}`},
		"calendar-category": {`{"id":"c1","name":"Music"}`},
	}}
	h2.install(t)
	buffered(t, h2, plain, text)
	h2.step(t)
	if h2.failed != "" || len(h2.upserts) != 0 {
		t.Fatalf("unchanged feed: failed %q, upserts %d: %+v", h2.failed, len(h2.upserts), h2.upserts)
	}

	// a changed end time updates the event and resends its slug, name and attributes
	moved := plain
	moved.Ev.End = start.Add(3 * time.Hour)
	h3 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r3", lists: map[string][]string{
		"calendar-event": {existingJSON(plain, "open-mic", `,"end_at":"2027-06-12T14:00:00+02:00","description":"Bring a friend.","venue":["v1"],"category":["c1"],"page_sections":[{"k":1}],"price":"Free"`)},
		"calendar-venue": {`{"id":"v1","name":"Town Hall"}`}, "calendar-category": {`{"id":"c1","name":"Music"}`},
	}}
	h3.install(t)
	buffered(t, h3, moved)
	h3.step(t)
	if h3.failed != "" || len(h3.upserts) != 1 {
		t.Fatalf("changed end: failed %q, upserts %d", h3.failed, len(h3.upserts))
	}
	up := h3.upserts[0]
	a = attrsOf(t, up)
	if up.Slug != "open-mic" || up.Name != "Open mic" || up.Locale != "en" || up.SourceKey != plain.Key || attrText(t, a, "end_at") != "2027-06-12T13:00:00Z" ||
		string(a["page_sections"]) != `[{"k":1}]` || attrText(t, a, "price") != "Free" || a["source_url"] != nil {
		t.Errorf("update: %+v %s", up, up.Attributes)
	}

	// the owner set a time zone on the event: its stored times are read in that
	// zone, so an unchanged feed stages nothing and a change keeps the instant
	ny := record("google", "src", "ny", importEvent{Title: "Open mic", Start: start, End: start.Add(2 * time.Hour)})
	nyJSON := strings.Replace(existingJSON(ny, "ny-mic", `,"time_zone":"America/New_York","end_at":"2027-06-12T08:00"`), "2027-06-12T12:00:00+02:00", "2027-06-12T06:00", 1)
	h5 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r5", lists: map[string][]string{"calendar-event": {nyJSON}}}
	h5.install(t)
	buffered(t, h5, ny)
	h5.step(t)
	if h5.failed != "" || len(h5.upserts) != 0 {
		t.Fatalf("own zone, same feed: failed %q, upserts %+v", h5.failed, h5.upserts)
	}
	ny.Ev.End = start.Add(3 * time.Hour)
	h6 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r6", lists: h5.lists}
	h6.install(t)
	buffered(t, h6, ny)
	h6.step(t)
	if len(h6.upserts) != 1 {
		t.Fatalf("own zone, moved end: %+v", h6.upserts)
	}
	a = attrsOf(t, h6.upserts[0])
	if attrText(t, a, "start_at") != "2027-06-12T06:00:00-04:00" || attrText(t, a, "end_at") != "2027-06-12T09:00:00-04:00" || attrText(t, a, "time_zone") != "America/New_York" {
		t.Errorf("own zone update: %s", h6.upserts[0].Attributes)
	}

	// an all-day event keeps the feed's date in the owner's zone, however far
	// west of the site zone it is, and a second import stages nothing
	berlin, _ := time.LoadLocation("Europe/Berlin")
	day := record("google", "src", "ny-day", importEvent{Title: "Fair", Start: time.Date(2029, 10, 31, 0, 0, 0, 0, berlin), End: time.Date(2029, 11, 1, 0, 0, 0, 0, berlin), AllDay: true})
	dayJSON := strings.Replace(existingJSON(day, "ny-day", `,"time_zone":"America/New_York","all_day":true,"end_at":"2029-11-01T00:00"`), "2027-06-12T12:00:00+02:00", "2029-10-31T00:00", 1)
	h7 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r7", lists: map[string][]string{"calendar-event": {dayJSON}}}
	h7.install(t)
	buffered(t, h7, day)
	h7.step(t)
	if h7.failed != "" || len(h7.upserts) != 0 {
		t.Fatalf("all day, own zone: failed %q, upserts %+v", h7.failed, h7.upserts)
	}
	if a := buildCreateAttrs(day.Ev, time.UTC, "google", "k", "", ""); attrText(t, a, "start_at") != "2029-10-31T00:00:00Z" || attrText(t, a, "end_at") != "2029-11-01T00:00:00Z" {
		t.Errorf("all day dates: %s", a["start_at"])
	}

	// a changed title updates too
	renamed := plain
	renamed.Ev.Title = "Open mic night"
	h4 := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r4", lists: h2.lists}
	h4.install(t)
	buffered(t, h4, renamed)
	h4.step(t)
	if len(h4.upserts) != 1 || h4.upserts[0].Name != "Open mic night" || h4.upserts[0].Slug != "open-mic" {
		t.Errorf("rename: %+v", h4.upserts)
	}
}

func TestImportStagingEdges(t *testing.T) {
	start := time.Date(2027, 6, 12, 10, 0, 0, 0, time.UTC)
	rec := record("ical", "One", "a@example.org", importEvent{Title: "A", Start: start})

	t.Run("nothing buffered finishes with zeros", func(t *testing.T) {
		h := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r"}
		h.install(t)
		h.step(t)
		if h.failed != "" || len(h.upserts) != 0 || h.finished == nil {
			t.Errorf("failed %q upserts %d finished %v", h.failed, len(h.upserts), h.finished)
		}
	})
	t.Run("a refused upsert is counted and the rest goes on", func(t *testing.T) {
		h := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r", upErr: &sdk.HostError{Code: sdk.ErrInvalidArgument}}
		h.install(t)
		buffered(t, h, rec, record("ical", "One", "b@example.org", importEvent{Title: "B", Start: start}))
		h.step(t)
		if h.failed != "" || len(h.upserts) != 2 {
			t.Errorf("failed %q upserts %d", h.failed, len(h.upserts))
		}
	})
	t.Run("manual import_url stages through the same path", func(t *testing.T) {
		h := &runHost{name: runEditDates, runID: "r", params: map[string]any{"action": "import_url"}}
		h.install(t)
		buffered(t, h, rec)
		h.step(t)
		if h.failed != "" || len(h.upserts) != 1 || attrsOf(t, h.upserts[0])["source_url"] != nil {
			t.Errorf("failed %q upserts %+v", h.failed, h.upserts)
		}
	})
	t.Run("an existing event of another kind is not matched", func(t *testing.T) {
		other := strings.Replace(existingJSON(rec, "a", ""), `"source_kind":"ical"`, `"source_kind":"manual"`, 1)
		h := &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "r", lists: map[string][]string{"calendar-event": {other}}}
		h.install(t)
		buffered(t, h, rec)
		h.step(t)
		if len(h.upserts) != 1 || h.upserts[0].Slug != "" {
			t.Errorf("upserts %+v", h.upserts)
		}
	})
}

func TestSameImport(t *testing.T) {
	m := func(s string) map[string]json.RawMessage {
		var out map[string]json.RawMessage
		if err := json.Unmarshal([]byte(s), &out); err != nil {
			t.Fatal(err)
		}
		return out
	}
	if !sameImport(m(`{"start_at":"2027-06-12T12:00:00+02:00","a":[1]}`), m(`{"start_at":"2027-06-12T10:00:00Z","a":[1]}`), time.UTC) {
		t.Error("the same instant in another offset is the same")
	}
	if sameImport(m(`{"start_at":"2027-06-12T12:00:00+02:00"}`), m(`{"start_at":"2027-06-12T12:00:00Z"}`), time.UTC) {
		t.Error("another instant differs")
	}
	if sameImport(m(`{"a":1}`), m(`{"a":1,"b":"x"}`), time.UTC) || sameImport(m(`{"a":"x"}`), m(`{"a":"y"}`), time.UTC) {
		t.Error("a missing or different attribute differs")
	}
	if !sameImport(m(`{"a":1,"extra":true}`), m(`{"a":1}`), time.UTC) {
		t.Error("attributes the import does not set are ignored")
	}
}

// The schema types start_at/end_at as date-time, so core takes strict RFC 3339
// only: the importer writes the local time with its offset.
func TestImportedTimesAreEditorLoadable(t *testing.T) {
	berlin, _ := time.LoadLocation("Europe/Berlin")
	london, _ := time.LoadLocation("Europe/London")
	cases := []struct {
		name       string
		ev         importEvent
		start, end string
	}{
		{"winter", importEvent{Start: time.Date(2026, 12, 20, 20, 0, 0, 0, time.UTC), End: time.Date(2026, 12, 20, 22, 0, 0, 0, time.UTC)}, "2026-12-20T21:00:00+01:00", "2026-12-20T23:00:00+01:00"},
		{"summer", importEvent{Start: time.Date(2027, 6, 12, 18, 0, 0, 0, time.UTC)}, "2027-06-12T20:00:00+02:00", ""},
		// the clocks change on 2027-03-28: 01:30 UTC+1 and 03:30 UTC+2 are one hour apart
		{"across the change", importEvent{Start: time.Date(2027, 3, 27, 23, 30, 0, 0, time.UTC), End: time.Date(2027, 3, 28, 1, 30, 0, 0, time.UTC)}, "2027-03-28T00:30:00+01:00", "2027-03-28T03:30:00+02:00"},
		// a feed in another zone: 19:00 in London is 20:00 in Berlin
		{"feed zone", importEvent{Start: time.Date(2027, 6, 12, 19, 0, 0, 0, london)}, "2027-06-12T20:00:00+02:00", ""},
		{"all day", importEvent{Start: time.Date(2027, 7, 1, 0, 0, 0, 0, berlin), End: time.Date(2027, 7, 2, 0, 0, 0, 0, berlin), AllDay: true}, "2027-07-01T00:00:00+02:00", "2027-07-02T00:00:00+02:00"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			a := buildCreateAttrs(c.ev, berlin, "google", "k", "", "")
			if got := attrText(t, a, "start_at"); got != c.start {
				t.Errorf("start_at = %q, want %q", got, c.start)
			}
			if got := attrText(t, a, "end_at"); got != c.end {
				t.Errorf("end_at = %q, want %q", got, c.end)
			}
			for k, v := range map[string]string{"start_at": c.start, "end_at": c.end} {
				if v != "" && !schemaAcceptsTime(t, k, v) {
					t.Errorf("the schema refuses %s %q", k, v)
				}
			}
			// what the plugin and the index read back is the same instant
			back, ok := parseTime(c.start, berlin)
			if !ok || !back.Equal(c.ev.Start) {
				t.Errorf("parseTime(%q) = %v, want %v", c.start, back, c.ev.Start)
			}
			// and a second import of the same event is unchanged
			if !sameImport(a, a, berlin) {
				t.Error("an import differs from itself")
			}
			raw, _ := json.Marshal(c.start)
			if !sameImport(map[string]json.RawMessage{"start_at": raw}, map[string]json.RawMessage{"start_at": a["start_at"]}, berlin) {
				t.Error("the stored form differs from the wanted one")
			}
		})
	}
}

func TestEditorTimeRoundsToTheMinute(t *testing.T) {
	for in, want := range map[time.Time]string{
		time.Date(2027, 6, 12, 18, 0, 29, 0, time.UTC):  "2027-06-12T20:00:00+02:00",
		time.Date(2027, 6, 12, 18, 0, 30, 0, time.UTC):  "2027-06-12T20:01:00+02:00",
		time.Date(2027, 6, 12, 17, 59, 45, 0, time.UTC): "2027-06-12T20:00:00+02:00",
	} {
		if got := editorTime(in, time.FixedZone("x", 7200)); got != want {
			t.Errorf("editorTime(%v) = %q, want %q", in, got, want)
		}
	}
}

// fixedNow pins the clock the transform phase reads.
func fixedNow(t *testing.T, at time.Time) {
	t.Helper()
	now = func() time.Time { return at }
	t.Cleanup(func() { now = time.Now })
}

// publishedBy is the entities.list answer for the events these upserts created.
func publishedBy(ups []sdk.EntityUpsert) []string {
	var out []string
	for i, up := range ups {
		out = append(out, fmt.Sprintf(`{"id":"p%d","schema_slug":"calendar-event","slug":"e%d","name":%q,"locale":"en","status":"published","attributes":{"start_at":"2027-06-12T10:00:00Z","source_kind":"google","source_uid":%q}}`,
			i, i, up.Name, up.SourceKey))
	}
	return out
}

func scheduledHost(published []string) *runHost {
	return &runHost{name: runScheduledImport, runID: "rs", created: true,
		settings: `{"import_sources":[{"name":"Town","kind":"google","url":"` + gcalURL + `","schedule":"daily"}]}`,
		pages:    map[string]fakePage{gcalURL: {status: 200, body: feedOf(250, "g")}},
		lists:    map[string][]string{"calendar-event": published}}
}

func TestScheduledImportPagesOverDays(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC))
	var published []string
	seen := map[string]bool{}
	for i, want := range []struct {
		created int
		line    string
	}{
		{100, "Imported 100 events, 150 skipped"},
		{100, "Imported 100 events, 150 skipped"},
		{50, "Imported 50 events, 200 skipped"},
	} {
		h := scheduledHost(published)
		h.install(t)
		h.runFetch(t)
		h.phase = "transform"
		h.step(t)
		if h.failed != "" || len(h.upserts) != want.created {
			t.Fatalf("run %d: failed %q, %d upserts, want %d", i+1, h.failed, len(h.upserts), want.created)
		}
		if !strings.HasPrefix(h.progress, want.line) {
			t.Errorf("run %d: progress %q, want prefix %q", i+1, h.progress, want.line)
		}
		for _, up := range h.upserts {
			if seen[up.SourceKey] {
				t.Errorf("run %d: %s staged twice", i+1, up.SourceKey)
			}
			seen[up.SourceKey] = true
		}
		published = append(published, publishedBy(h.upserts)...)
	}
	if len(seen) != 250 {
		t.Errorf("%d events in all, want 250", len(seen))
	}
}

func TestManualImportCap(t *testing.T) {
	h := manualRun(gcalURL, "google", "Town", map[string]fakePage{gcalURL: {status: 200, body: feedOf(250, "g")}})
	h.created = true
	h.install(t)
	h.runFetch(t)
	h.phase = "transform"
	h.step(t)
	if h.failed != "" || len(h.upserts) != 200 {
		t.Fatalf("failed %q, %d upserts", h.failed, len(h.upserts))
	}
	if !strings.HasPrefix(h.progress, "Imported 200 events, 50 skipped") || !strings.Contains(h.progress, "50 left for the next run") {
		t.Errorf("progress %q", h.progress)
	}
}

func TestScheduledSkipsPastAndNeverUpdates(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 14, 12, 0, 0, 0, time.UTC))
	ended := time.Date(2027, 6, 12, 10, 0, 0, 0, time.UTC) // two days ago
	recent := time.Date(2027, 6, 14, 1, 0, 0, 0, time.UTC) // ended less than a day ago
	past := record("google", "Town", "past", importEvent{Title: "Past", Start: ended, End: ended.Add(time.Hour)})
	fresh := record("google", "Town", "recent", importEvent{Title: "Recent", Start: recent, End: recent.Add(time.Hour)})
	known := record("google", "Town", "known", importEvent{Title: "Known renamed", Start: recent.Add(48 * time.Hour)})
	h := &runHost{name: runScheduledImport, runID: "rp", created: true, lists: map[string][]string{"calendar-event": {existingJSON(known, "known", "")}}}
	h.install(t)
	buffered(t, h, past, fresh, known)
	h.step(t)
	if h.failed != "" || len(h.upserts) != 1 || h.upserts[0].SourceKey != fresh.Key {
		t.Fatalf("failed %q, upserts %+v", h.failed, h.upserts)
	}
	if !strings.HasPrefix(h.progress, "Imported 1 events, 2 skipped") {
		t.Errorf("progress %q", h.progress)
	}
}

func TestTransformSteps(t *testing.T) {
	h := manualRun(gcalURL, "google", "Town", map[string]fakePage{gcalURL: {status: 200, body: feedOf(120, "g")}})
	h.created = true
	h.install(t)
	h.runFetch(t)
	h.phase = "transform"
	h.step(t)
	if h.failed != "" || h.steps < 3 {
		t.Fatalf("failed %q after %d steps", h.failed, h.steps)
	}
	keys := map[string]bool{}
	for _, up := range h.upserts {
		keys[up.SourceKey] = true
	}
	if len(h.upserts) != 120 || len(keys) != 120 {
		t.Errorf("%d upserts, %d distinct keys, want 120", len(h.upserts), len(keys))
	}
	if h.cursor != "" && len(h.cursor) > maxImportCursor {
		t.Errorf("checkpoint of %d bytes", len(h.cursor))
	}
}

func TestScheduledCreateIsPublishedAndSafe(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 1, 12, 0, 0, 0, time.UTC))
	start := time.Date(2027, 6, 12, 18, 0, 0, 0, time.UTC)
	rec := record("google", "Town", "x", importEvent{Title: "Jam www.example.org", Start: start, Description: "Info https://example.org/a\nDoors: 7pm", TicketURL: "https://Tickets.example.com/a", Website: "http://insecure.example.com"})

	for _, name := range []string{runScheduledImport, runEditDates} {
		h := &runHost{name: name, runID: "rc", created: true}
		if name == runEditDates {
			h.params = map[string]any{"action": "import_url"}
		}
		h.install(t)
		buffered(t, h, rec)
		h.step(t)
		if h.failed != "" || len(h.upserts) != 1 {
			t.Fatalf("%s: failed %q, upserts %d", name, h.failed, len(h.upserts))
		}
		up := h.upserts[0]
		a := attrsOf(t, up)
		if name == runScheduledImport {
			if up.Status != "published" || up.Slug != "jam-2027-06-12-"+rec.Key[len(rec.Key)-6:] || up.Name != "Jam" ||
				attrText(t, a, "description") != "Info\nDoors: 7pm" || attrText(t, a, "ticket_url") != "https://tickets.example.com/a" || a["website"] != nil {
				t.Errorf("scheduled create: %+v %s", up, up.Attributes)
			}
			continue
		}
		if up.Status != "" || up.Slug != "" || up.Name != "Jam www.example.org" ||
			attrText(t, a, "description") != "Info https://example.org/a\nDoors: 7pm" || attrText(t, a, "website") != "http://insecure.example.com" {
			t.Errorf("manual create: %+v %s", up, up.Attributes)
		}
	}
}

func TestScheduledRunStagesNothingWhenItCannotCheckEveryEvent(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 14, 12, 0, 0, 0, time.UTC))
	start := time.Date(2027, 6, 20, 10, 0, 0, 0, time.UTC)
	fresh := record("google", "Town", "fresh", importEvent{Title: "Fresh", Start: start})
	h := &runHost{name: runScheduledImport, runID: "rc", created: true, endless: true}
	h.install(t)
	buffered(t, h, fresh)
	h.step(t)
	if len(h.upserts) != 0 || !strings.Contains(h.failed, "more imported events") {
		t.Errorf("failed %q, upserts %+v", h.failed, h.upserts)
	}
	// a manual run is reviewed, so it still stages
	h = &runHost{name: runEditDates, params: map[string]any{"action": "import_url"}, runID: "rm", created: true, endless: true}
	h.install(t)
	buffered(t, h, fresh)
	h.step(t)
	if len(h.upserts) != 1 {
		t.Errorf("manual run staged %d", len(h.upserts))
	}
}

func TestScheduledProgressWarnsAboutDeletedEvents(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 14, 12, 0, 0, 0, time.UTC))
	h := &runHost{name: runScheduledImport, runID: "rw", created: true}
	h.install(t)
	buffered(t, h, record("google", "Town", "a", importEvent{Title: "A", Start: time.Date(2027, 6, 20, 10, 0, 0, 0, time.UTC)}))
	h.step(t)
	if !strings.Contains(h.progress, "deleted or unpublished") {
		t.Errorf("progress %q", h.progress)
	}
}

func TestScheduledRunDoesNotDuplicateEventsOfARenamedSource(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 14, 12, 0, 0, 0, time.UTC))
	start := time.Date(2027, 6, 20, 10, 0, 0, 0, time.UTC)
	before := record("google", "City council", "uid-1", importEvent{Title: "Meeting", Start: start})
	after := record("google", "City council events", "uid-1", importEvent{Title: "Meeting", Start: start})
	other := record("google", "City council events", "uid-2", importEvent{Title: "Other", Start: start})
	h := &runHost{name: runScheduledImport, runID: "rr", created: true, lists: map[string][]string{"calendar-event": {existingJSON(before, "meeting", "")}}}
	h.install(t)
	buffered(t, h, after, other)
	h.step(t)
	if h.failed != "" || len(h.upserts) != 1 || h.upserts[0].SourceKey != other.Key {
		t.Fatalf("failed %q, upserts %+v", h.failed, h.upserts)
	}
}

func TestTransformStepProgressesWhenThePreloadUsesTheBudget(t *testing.T) {
	fixedNow(t, time.Date(2027, 6, 14, 12, 0, 0, 0, time.UTC))
	at := time.Now()
	clock = func() time.Time { at = at.Add(stepBudget); return at } // every look at the clock burns the budget
	t.Cleanup(func() { clock = time.Now })
	start := time.Date(2027, 6, 20, 10, 0, 0, 0, time.UTC)
	h := &runHost{name: runScheduledImport, runID: "rb", created: true}
	h.install(t)
	buffered(t, h, record("google", "Town", "a", importEvent{Title: "A", Start: start}), record("google", "Town", "b", importEvent{Title: "B", Start: start}))
	h.step(t)
	if h.failed != "" || len(h.upserts) != 2 || h.finished == nil {
		t.Errorf("failed %q, %d upserts, finished %v", h.failed, len(h.upserts), h.finished)
	}
}
