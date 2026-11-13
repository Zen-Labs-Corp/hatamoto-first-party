package main

import (
	"encoding/json"
	"reflect"
	"testing"

	"hatamoto.dev/sdk"
)

// slugHost adds entities.slug_taken to a runHost: the slugs in taken answer true.
func slugHost(t *testing.T, h *runHost, taken ...string) {
	t.Helper()
	h.install(t)
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == "entities.slug_taken" {
			h.calls = append(h.calls, fn)
			b, _ := json.Marshal(args)
			var a struct{ Slug string }
			_ = json.Unmarshal(b, &a)
			for _, s := range taken {
				if s == a.Slug {
					return json.Unmarshal([]byte(`{"taken":true}`), out)
				}
			}
			return json.Unmarshal([]byte(`{"taken":false}`), out)
		}
		return inner(fn, args, out)
	}
}

const dupSource = `{"id":"e1","schema_slug":"calendar-event","slug":"yoga","name":"Yoga","locale":"en","status":"published","attributes":{
"start_at":"2026-02-17T18:00","end_at":"2026-02-17T19:00","repeat":"weekly","repeat_until":"2026-06-30","repeat_extra_dates":["2026-12-24"],
"repeat_skip_dates":["2026-03-10"],"repeat_overrides":["2026-03-03|2026-03-03T19:00|2026-03-03T20:00"],
"source_uid":"ical-77","source_kind":"ical","source_url":"https://example.org/e/77","venue":["v1"],"page_sections":[{"k":1}]}}`

func TestDuplicateEventRun(t *testing.T) {
	h := &runHost{name: runDuplicate, runID: "run-5", params: map[string]any{"event": "yoga", "shift_days": "7"}, entities: []string{dupSource}}
	slugHost(t, h, "yoga-copy")
	h.step(t)
	if h.failed != "" || len(h.upserts) != 1 {
		t.Fatalf("failed = %q, upserts = %d", h.failed, len(h.upserts))
	}
	up := h.upserts[0]
	if up.SourceKey != "dup-run-5" || up.Slug != "yoga-copy-2" || up.Name != "Yoga (copy)" || up.Schema != "calendar-event" || up.Locale != "en" {
		t.Errorf("upsert = %+v", up)
	}
	var orig, got map[string]json.RawMessage
	var d entityDTO
	_ = json.Unmarshal([]byte(dupSource), &d)
	_ = json.Unmarshal(d.Attributes, &orig)
	got = attrsOf(t, up)
	want := cloneAttrs(orig)
	for k, v := range map[string]string{
		"start_at": `"2026-02-24T18:00"`, "end_at": `"2026-02-24T19:00"`, "repeat_until": `"2026-07-07"`,
		"repeat_extra_dates": `["2026-12-31"]`, "repeat_skip_dates": `["2026-03-17"]`,
		"repeat_overrides": `["2026-03-10|2026-03-10T19:00|2026-03-10T20:00"]`,
		"source_uid":       `"dup-run-5"`, "source_kind": `"manual"`,
	} {
		want[k] = json.RawMessage(v)
	}
	delete(want, "source_url")
	if !reflect.DeepEqual(got, want) {
		t.Errorf("attributes\n got %s\nwant %s", up.Attributes, mustJSON(want))
	}

	// a second step of the same run stages the same key
	h2 := &runHost{name: runDuplicate, runID: "run-5", params: h.params, entities: []string{dupSource}}
	slugHost(t, h2, "yoga-copy")
	h2.step(t)
	if len(h2.upserts) != 1 || h2.upserts[0].SourceKey != up.SourceKey || h2.upserts[0].Slug != up.Slug {
		t.Errorf("re-step = %+v", h2.upserts)
	}
}

func TestDuplicateEventDefaultsAndFailures(t *testing.T) {
	h := &runHost{name: runDuplicate, runID: "r", params: map[string]any{"event": "yoga"}, entities: []string{dupSource}}
	slugHost(t, h)
	h.step(t)
	if len(h.upserts) != 1 || h.upserts[0].Slug != "yoga-copy" || string(attrsOf(t, h.upserts[0])["start_at"]) != `"2026-02-17T18:00"` {
		t.Fatalf("default shift: %+v failed %q", h.upserts, h.failed)
	}
	for name, p := range map[string]map[string]any{
		"missing event": {"event": "nope"},
		"bad shift":     {"event": "yoga", "shift_days": "soon"},
		"huge shift":    {"event": "yoga", "shift_days": float64(99999)},
	} {
		h := &runHost{name: runDuplicate, runID: "r", params: p, entities: []string{dupSource}}
		slugHost(t, h)
		h.step(t)
		if h.failed == "" || len(h.upserts) != 0 {
			t.Errorf("%s: failed = %q, upserts = %d", name, h.failed, len(h.upserts))
		}
	}
	h = &runHost{name: runDuplicate, runID: "r", params: map[string]any{"event": "yoga"}, entities: []string{dupSource}, upErr: &sdk.HostError{Code: "internal", Message: "secret"}}
	slugHost(t, h)
	h.step(t)
	if h.failed == "" || len(h.upserts) != 1 {
		t.Errorf("upsert error: failed = %q", h.failed)
	}
}

func TestShiftText(t *testing.T) {
	for in, want := range map[string]string{
		"2026-02-28": "2026-03-07", "2026-02-28T09:05": "2026-03-07T09:05",
		"2026-02-28T09:05:30": "2026-03-07T09:05:30", "2026-02-28T09:05:00+01:00": "2026-03-07T09:05:00+01:00", "soon": "soon",
	} {
		if got := shiftText(in, 7); got != want {
			t.Errorf("shiftText(%q) = %q, want %q", in, got, want)
		}
	}
}
