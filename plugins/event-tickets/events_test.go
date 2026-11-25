package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

type call struct {
	fn   string
	sql  string
	args []any
}

// eventHost fakes settings, entities.get and the db for the entity hooks.
type eventHost struct {
	entity   string // entities.get answer; "" = not found
	getErr   error
	hasTypes bool
	calls    []call
}

func (h *eventHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		h.calls = append(h.calls, call{fn, sql, ar})
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			if h.getErr != nil {
				return h.getErr
			}
			if h.entity == "" {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			return jsonInto(h.entity, out)
		case strings.Contains(sql, "EXISTS"):
			return jsonInto(`{"rows":[[`+jsonText(h.hasTypes)+`]]}`, out)
		case strings.HasPrefix(sql, "INSERT INTO events"), strings.HasPrefix(sql, "UPDATE "):
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
}

func jsonText(v any) string { b, _ := json.Marshal(v); return string(b) }

func (h *eventHost) used(prefix string) *call {
	for i, c := range h.calls {
		if c.fn == prefix || strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

const gigEntity = `{"id":"e1","schema_slug":"calendar-event","name":"Gig","status":"published","url":"https://example.org/events/gig",
"attributes":{"start_at":"2027-03-01T20:00","end_at":"2027-03-01T22:00","time_zone":"Europe/Paris","online_url":"https://meet.example.org/x","join_link_public":"false","attendance_mode":"hybrid"}}`

func fire(t *testing.T, kind sdk.Action[sdk.EntityEvent], ev sdk.EntityEvent) {
	t.Helper()
	in, _ := json.Marshal(ev)
	if _, err := guest.Handle(kind.Export(), in); err != nil {
		t.Fatalf("hook returned an error: %v", err)
	}
}

func TestHookIgnoresOtherTypes(t *testing.T) {
	h := &eventHost{}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "p1", SchemaSlug: "page"})
	fire(t, sdk.EntityDeleted, sdk.EntityEvent{ID: "p1", SchemaSlug: "page"})
	for _, c := range h.calls {
		if c.fn != sdk.FnSettingsGet {
			t.Errorf("unexpected host call %q %q", c.fn, c.sql)
		}
	}
}

func TestHookSkipsEventsWithoutTypes(t *testing.T) {
	h := &eventHost{entity: gigEntity}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if h.used(sdk.FnEntitiesGet) != nil || h.used("INSERT INTO events") != nil {
		t.Error("an event without ticket types must not be read or stored")
	}
}

func TestHookSnapshot(t *testing.T) {
	h := &eventHost{entity: gigEntity, hasTypes: true}
	h.install(t)
	fire(t, sdk.EntityPublished, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	c := h.used("INSERT INTO events")
	if c == nil {
		t.Fatal("no snapshot written")
	}
	want := []any{"e1", "calendar-event", "Gig", "2027-03-01T19:00:00Z", "2027-03-01T21:00:00Z", "published"}
	if jsonText(c.args) != jsonText(want) {
		t.Errorf("args %s, want %s", jsonText(c.args), jsonText(want))
	}
	if strings.Contains(jsonText(c.args), "meet.example.org") {
		t.Error("the join link must not be stored")
	}
}

func TestHookUnpublishedAndErrors(t *testing.T) {
	h := &eventHost{hasTypes: true} // entity not found
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if c := h.used("UPDATE events SET status = 'unpublished'"); c == nil || c.args[0] != "e1" {
		t.Errorf("unpublished not recorded: %+v", h.calls)
	}
	h = &eventHost{hasTypes: true, getErr: errors.New("down")}
	h.install(t)
	fire(t, sdk.EntityUpdated, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if h.used("INSERT INTO events") != nil || h.used("UPDATE events") != nil {
		t.Error("a host error must change nothing")
	}
}

func TestHookDeletedClosesSales(t *testing.T) {
	h := &eventHost{}
	h.install(t)
	fire(t, sdk.EntityDeleted, sdk.EntityEvent{ID: "e1", SchemaSlug: "calendar-event"})
	if h.used("UPDATE events SET status = 'deleted'") == nil {
		t.Error("the snapshot is not marked deleted")
	}
	c := h.used("UPDATE ticket_types SET on_sale = false")
	if c == nil || c.args[0] != "e1" {
		t.Error("sales are not closed")
	}
}

func TestReadEvent(t *testing.T) {
	h := &eventHost{entity: gigEntity}
	h.install(t)
	e, ok, err := readEvent("e1", defaultSettings())
	if err != nil || !ok {
		t.Fatalf("%v %v", ok, err)
	}
	if e.Name != "Gig" || e.OnlineURL != "https://meet.example.org/x" || e.JoinPublic || !e.HasEnd || e.Start.UTC().Hour() != 19 {
		t.Errorf("%+v", e)
	}
	for name, ent := range map[string]string{
		"other type": strings.Replace(gigEntity, "calendar-event", "page", 1),
		"draft":      strings.Replace(gigEntity, `"published"`, `"draft"`, 1),
	} {
		h.entity = ent
		if _, ok, err := readEvent("e1", defaultSettings()); ok || err != nil {
			t.Errorf("%s: ok=%v err=%v", name, ok, err)
		}
	}
	h.entity, h.getErr = "", errors.New("down")
	if _, _, err := readEvent("e1", defaultSettings()); err == nil {
		t.Error("a host error must be reported")
	}
}

func TestJoinPublic(t *testing.T) {
	for name, c := range map[string]struct {
		attrs map[string]any
		want  bool
	}{
		"absent":    {map[string]any{}, true},
		"nil":       {nil, true},
		"true":      {map[string]any{"join_link_public": true}, true},
		"false":     {map[string]any{"join_link_public": false}, false},
		"text":      {map[string]any{"join_link_public": "false"}, false},
		"zero text": {map[string]any{"join_link_public": "0"}, false},
		"yes text":  {map[string]any{"join_link_public": "yes"}, true},
		"number":    {map[string]any{"join_link_public": float64(1)}, true},
	} {
		if got := joinPublic(c.attrs); got != c.want {
			t.Errorf("%s: %v, want %v", name, got, c.want)
		}
	}
}

func TestOriginOf(t *testing.T) {
	for _, c := range []struct{ url, site, want string }{
		{"https://example.org/events/gig", "https://other.example", "https://example.org"},
		{"/events/gig", "https://site.example", "https://site.example"},
		{"/events/gig", "", ""},
		{"javascript://x/%0aalert(1)", "", ""},
		{"", "http://localhost:8080", "http://localhost:8080"},
	} {
		if got := originOf(c.url, c.site); got != c.want {
			t.Errorf("originOf(%q, %q) = %q, want %q", c.url, c.site, got, c.want)
		}
	}
}

func TestOpen(t *testing.T) {
	at := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	e := event{Status: "published", Start: at.Add(time.Hour)}
	if !e.open(at) {
		t.Error("an event that starts later is open")
	}
	e.Start = at.Add(-time.Hour)
	if e.open(at) {
		t.Error("a past event without an end is closed")
	}
	e.End, e.HasEnd = at.Add(time.Hour), true
	if !e.open(at) {
		t.Error("an event still running is open")
	}
	e.AllDay, e.HasEnd = true, false
	e.Start = time.Date(2027, 3, 1, 0, 0, 0, 0, time.UTC)
	if !e.open(at) {
		t.Error("an all-day event is open all day")
	}
	for _, st := range []string{"cancelled", "postponed"} {
		e := event{Status: "published", EventStatus: st, Start: at.Add(time.Hour)}
		if e.open(at) {
			t.Errorf("%s event is open", st)
		}
	}
	if (event{Status: "published"}).open(at) || (event{Status: "draft", Start: at.Add(time.Hour)}).open(at) {
		t.Error("no start or not published is closed")
	}
}

func TestIDOK(t *testing.T) {
	for id, want := range map[string]bool{"abc-1_Z": true, "": false, "a b": false, "a/b": false, strings.Repeat("a", 65): false} {
		if idOK(id) != want {
			t.Errorf("idOK(%q) != %v", id, want)
		}
	}
}
