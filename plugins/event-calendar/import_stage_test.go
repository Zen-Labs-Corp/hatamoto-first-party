package main

import (
	"encoding/json"
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
					t.Errorf("%s %q is not RFC 3339", k, v)
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
	if !sameImport(m(`{"start_at":"2027-06-12T12:00:00+02:00","a":[1]}`), m(`{"start_at":"2027-06-12T10:00:00Z","a":[1]}`)) {
		t.Error("the same instant in another offset is the same")
	}
	if sameImport(m(`{"start_at":"2027-06-12T12:00:00+02:00"}`), m(`{"start_at":"2027-06-12T12:00:00Z"}`)) {
		t.Error("another instant differs")
	}
	if sameImport(m(`{"a":1}`), m(`{"a":1,"b":"x"}`)) || sameImport(m(`{"a":"x"}`), m(`{"a":"y"}`)) {
		t.Error("a missing or different attribute differs")
	}
	if !sameImport(m(`{"a":1,"extra":true}`), m(`{"a":1}`)) {
		t.Error("attributes the import does not set are ignored")
	}
}
