package main

import (
	"encoding/base64"
	"encoding/json"
	"reflect"
	"sort"
	"strconv"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// runHost fakes the host side of a run step: run.info, entities.get and the
// run.* and changeset calls, which it records.
type runHost struct {
	name     string
	runID    string
	params   map[string]any
	entities []string
	getErr   error
	upErr    error
	created  bool // the host reports the staged upsert as a new object
	upserts  []sdk.EntityUpsert
	failed   string
	finished any
	calls    []string

	// settings is the answer of settings.get ("{}" when empty); phase is the
	// phase run.info reports ("transform" when empty).
	settings string
	phase    string
	pages    map[string]fakePage // http.fetch answers by address
	buffer   map[string][]byte
	cursor   string // the last run.checkpoint
	fetched  []string
	lists    map[string][]string // entities.list answers by content type
}

// fakePage is the answer of a fake http.fetch.
type fakePage struct {
	status    int
	body      string
	truncated bool
	err       error
}

func (h *runHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		h.calls = append(h.calls, fn)
		switch fn {
		case "settings.get":
			if h.settings != "" {
				return json.Unmarshal([]byte(h.settings), out)
			}
			return json.Unmarshal([]byte(`{}`), out)
		case "run.info":
			p, _ := json.Marshal(h.params)
			phase := h.phase
			if phase == "" {
				phase = "transform"
			}
			b, _ := json.Marshal(sdk.RunInfo{RunID: h.runID, Name: h.name, Phase: phase, Params: p, Cursor: h.cursor})
			return json.Unmarshal(b, out)
		case "run.checkpoint":
			h.cursor = args.(sdk.RunCheckpointArgs).Cursor
			return nil
		case "run.progress":
			return nil
		case "http.fetch":
			u := args.(map[string]any)["url"].(string)
			h.fetched = append(h.fetched, u)
			page, ok := h.pages[u]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrBlocked}
			}
			if page.err != nil {
				return page.err
			}
			b, _ := json.Marshal(map[string]any{"status": page.status, "body": page.body, "truncated": page.truncated})
			return json.Unmarshal(b, out)
		case "entities.list":
			// the whole list comes on the first page, whatever the cursor
			schema := args.(map[string]any)["schema_slug"].(string)
			if args.(map[string]any)["cursor"] != nil {
				return json.Unmarshal([]byte(`{"items":[]}`), out)
			}
			items := h.lists[schema]
			if items == nil {
				items = []string{}
			}
			return json.Unmarshal([]byte(`{"items":[`+strings.Join(items, ",")+`]}`), out)
		case "buffer.put":
			a := args.(sdk.BufferPutArgs)
			v, err := base64.StdEncoding.DecodeString(a.ValueB64)
			if err != nil {
				return err
			}
			if h.buffer == nil {
				h.buffer = map[string][]byte{}
			}
			h.buffer[a.Key] = v
			return nil
		case "buffer.get":
			v, ok := h.buffer[args.(sdk.BufferGetArgs).Key]
			b, _ := json.Marshal(sdk.BufferValue{Found: ok, ValueB64: base64.StdEncoding.EncodeToString(v)})
			return json.Unmarshal(b, out)
		case "buffer.list":
			a := args.(sdk.BufferListArgs)
			var keys []string
			for k := range h.buffer {
				if strings.HasPrefix(k, a.Prefix) && k > a.After {
					keys = append(keys, k)
				}
			}
			sort.Strings(keys)
			next := ""
			if a.Limit > 0 && len(keys) > a.Limit {
				keys = keys[:a.Limit]
				next = keys[len(keys)-1]
			}
			b, _ := json.Marshal(sdk.BufferKeys{Keys: keys, Next: next})
			return json.Unmarshal(b, out)
		case "run.fail":
			h.failed = args.(sdk.RunFailArgs).Reason
			return nil
		case "run.finish":
			h.finished = args
			return nil
		case "entities.get":
			if h.getErr != nil {
				return h.getErr
			}
			a := args.(map[string]any)
			for _, e := range h.entities {
				var d entityDTO
				_ = json.Unmarshal([]byte(e), &d)
				if d.Slug == a["slug"] && d.SchemaSlug == a["schema_slug"] {
					return json.Unmarshal([]byte(e), out)
				}
			}
			return &sdk.HostError{Code: sdk.ErrNotFound}
		case "changeset.entity.upsert":
			b, _ := json.Marshal(args)
			var up sdk.EntityUpsert
			_ = json.Unmarshal(b, &up)
			h.upserts = append(h.upserts, up)
			if h.upErr != nil {
				return h.upErr
			}
			return json.Unmarshal([]byte(`{"source_key":"`+up.SourceKey+`","created":`+strconv.FormatBool(h.created)+`}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (h *runHost) step(t *testing.T) {
	t.Helper()
	if _, err := onStep(sdk.RunStep{RunID: h.runID, Phase: sdk.RunPhaseTransform}); err != nil {
		t.Fatalf("step error: %v", err)
	}
}

const ownedEvent = `{"id":"e1","schema_slug":"calendar-event","slug":"yoga","name":"Yoga","locale":"en","status":"published","attributes":{
"start_at":"2026-02-17T18:00","repeat":"weekly","source_uid":"dup-run-1/yoga","source_kind":"manual","page_sections":[{"k":1}],
"repeat_skip_dates":["2026-03-10"],"repeat_overrides":["2026-03-03|2026-03-03T19:00"],"venue":["v1"]}}`

func attrsOf(t *testing.T, up sdk.EntityUpsert) map[string]json.RawMessage {
	t.Helper()
	var m map[string]json.RawMessage
	if err := json.Unmarshal(up.Attributes, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestSkipRestoreChangeStages(t *testing.T) {
	orig := map[string]json.RawMessage{}
	var d entityDTO
	_ = json.Unmarshal([]byte(ownedEvent), &d)
	_ = json.Unmarshal(d.Attributes, &orig)

	cases := []struct {
		name   string
		params map[string]any
		skips  string // expected repeat_skip_dates, "" = absent
		overs  string
	}{
		{"skip", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, `["2026-02-24","2026-03-10"]`, `["2026-03-03|2026-03-03T19:00"]`},
		{"skip drops an override of that day", map[string]any{"event": "yoga", "action": "skip", "date": "2026-03-03"}, `["2026-03-03","2026-03-10"]`, ``},
		{"restore a skip", map[string]any{"event": "yoga", "action": "restore", "date": "2026-03-10"}, ``, `["2026-03-03|2026-03-03T19:00"]`},
		{"restore a change", map[string]any{"event": "yoga", "action": "restore", "date": "2026-03-03"}, `["2026-03-10"]`, ``},
		{"change with times of day", map[string]any{"event": "yoga", "action": "change", "date": "2026-02-24", "start_at": "7:30", "end_at": "20:00"},
			`["2026-03-10"]`, `["2026-02-24|2026-02-24T07:30|2026-02-24T20:00","2026-03-03|2026-03-03T19:00"]`},
		{"change replaces the old change", map[string]any{"event": "yoga", "action": "change", "date": "2026-03-03", "start_at": "2026-03-03T20:15"},
			`["2026-03-10"]`, `["2026-03-03|2026-03-03T20:15"]`},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &runHost{name: runEditDates, runID: "run-9", params: c.params, entities: []string{ownedEvent}}
			h.install(t)
			h.step(t)
			if h.failed != "" || len(h.upserts) != 1 {
				t.Fatalf("failed = %q, upserts = %d", h.failed, len(h.upserts))
			}
			up := h.upserts[0]
			if up.SourceKey != "dup-run-1/yoga" || up.Schema != "calendar-event" || up.Slug != "yoga" || up.Name != "Yoga" || up.Locale != "en" {
				t.Errorf("upsert = %+v", up)
			}
			got := attrsOf(t, up)
			want := cloneAttrs(orig)
			for k, v := range map[string]string{"repeat_skip_dates": c.skips, "repeat_overrides": c.overs} {
				if v == "" {
					delete(want, k)
				} else {
					want[k] = json.RawMessage(v)
				}
			}
			if !reflect.DeepEqual(got, want) {
				t.Errorf("attributes\n got %s\nwant %s", up.Attributes, mustJSON(want))
			}
		})
	}
}

func mustJSON(v any) string { b, _ := json.Marshal(v); return string(b) }

func TestEditDatesFailures(t *testing.T) {
	cases := []struct {
		name   string
		params map[string]any
		event  string
		getErr error
		upErr  error
		reason string
	}{
		{"bad action", map[string]any{"event": "yoga", "action": "delete", "date": "2026-02-24"}, ownedEvent, nil, nil, "skip, change, restore or duplicate"},
		{"bad date", map[string]any{"event": "yoga", "action": "skip", "date": "24/02/2026"}, ownedEvent, nil, nil, "2026-03-03"},
		{"change without start", map[string]any{"event": "yoga", "action": "change", "date": "2026-02-24"}, ownedEvent, nil, nil, "start time"},
		{"bad end", map[string]any{"event": "yoga", "action": "change", "date": "2026-02-24", "start_at": "18:00", "end_at": "soon"}, ownedEvent, nil, nil, "end time"},
		{"bad slug", map[string]any{"event": "../x", "action": "skip", "date": "2026-02-24"}, ownedEvent, nil, nil, "slug"},
		{"missing event", map[string]any{"event": "nope", "action": "skip", "date": "2026-02-24"}, ownedEvent, nil, nil, "No published event"},
		{"draft event", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, strings.Replace(ownedEvent, `"published"`, `"draft"`, 1), nil, nil, "No published event"},
		{"parent", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, strings.Replace(ownedEvent, `"status"`, `"parent_id":"p","status"`, 1), nil, nil, "parent"},
		{"no key", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, strings.Replace(ownedEvent, `dup-run-1/yoga`, `abc`, 1), nil, nil, "Who owns this page"},
		{"host read error", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, ownedEvent, &sdk.HostError{Code: "internal", Message: "secret detail"}, nil, "could not be read"},
		{"not owned", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, ownedEvent, nil, &sdk.HostError{Code: sdk.ErrNotOwned}, "Who owns this page"},
		{"other upsert error", map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, ownedEvent, nil, &sdk.HostError{Code: "internal", Message: "secret detail"}, "could not be staged"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &runHost{name: runEditDates, params: c.params, entities: []string{c.event}, getErr: c.getErr, upErr: c.upErr}
			h.install(t)
			h.step(t)
			if !strings.Contains(h.failed, c.reason) || strings.Contains(h.failed, "secret") {
				t.Errorf("failed = %q, want it to contain %q", h.failed, c.reason)
			}
			if h.finished != nil {
				t.Errorf("a failed run finished: %v", h.finished)
			}
			if c.upErr == nil && len(h.upserts) != 0 {
				t.Errorf("staged %d items before failing", len(h.upserts))
			}
			if len(h.upserts) > 1 {
				t.Errorf("a not-owned upsert was retried: %d", len(h.upserts))
			}
		})
	}
}

// A source key that maps to no earlier object would stage a second event; the
// run must fail instead of finishing, which discards the changeset.
func TestEditDatesFailsWhenUpsertCreates(t *testing.T) {
	h := &runHost{name: runEditDates, params: map[string]any{"event": "yoga", "action": "skip", "date": "2026-02-24"}, entities: []string{ownedEvent}, created: true}
	h.install(t)
	h.step(t)
	if !strings.Contains(h.failed, "Who owns this page") || h.finished != nil {
		t.Errorf("failed = %q, finished = %v", h.failed, h.finished)
	}
}

func TestRunFetchPhaseFinishes(t *testing.T) {
	h := &runHost{name: runEditDates, phase: "fetch"}
	h.install(t)
	if _, err := onStep(sdk.RunStep{Phase: sdk.RunPhaseFetch}); err != nil {
		t.Fatal(err)
	}
	if len(h.calls) != 2 || h.calls[0] != "run.info" || h.calls[1] != "run.finish" {
		t.Errorf("calls = %v", h.calls)
	}
}

func TestOwnedKey(t *testing.T) {
	for _, c := range []struct{ uid, slug, want string }{
		{"dup-r1/yoga", "yoga", "dup-r1/yoga"},
		{" dup-r1/yoga ", "yoga", "dup-r1/yoga"},
		{"dup-r1/yoga-copy", "yoga", ""}, // made for another slug
		{"dup-r1/yoga", "yoga-copy", ""},
		{"dup-/yoga", "yoga", ""},
		{"dup-r1/", "", ""},
		{"dup-r1", "yoga", ""}, // the 0.4.0 shape is not accepted
		{"dup-r1/a/yoga", "yoga", "dup-r1/a/yoga"},
		{"dup-", "", ""}, {"", "yoga", ""}, {"abc-1/yoga", "yoga", ""},
	} {
		if k, ok := ownedKey(c.uid, c.slug); k != c.want || ok != (c.want != "") {
			t.Errorf("ownedKey(%q, %q) = %q, %v", c.uid, c.slug, k, ok)
		}
	}
}
