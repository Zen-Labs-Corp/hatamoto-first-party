package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// hookHost fakes the host for the hooks: settings, entities.list/get and db.
type hookHost struct {
	settings string
	docs     map[string]string // entities.get and the listing, by id
	order    []string          // listing order
	more     bool              // the listing never ends (next_cursor always set)
	listErr  error
	types    []string // when set, entities.list of any other type is not_found, as in core
	getErr   error
	dbErr    error
	indexed  map[string]string // what db.query answers for indexedIDs
	calls    []string          // every host function called
	execs    []dbCall
}

func (h *hookHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		h.calls = append(h.calls, fn)
		a, _ := args.(map[string]any)
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(h.settings), out)
		case "entities.list":
			if h.listErr != nil {
				return h.listErr
			}
			if h.types != nil && !slices.Contains(h.types, a["schema_slug"].(string)) {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			var items []string
			for _, id := range h.order {
				var d entityDTO
				_ = json.Unmarshal([]byte(h.docs[id]), &d)
				if d.SchemaSlug == a["schema_slug"] {
					items = append(items, h.docs[id])
				}
			}
			next := ""
			if h.more {
				next = "more"
			}
			return json.Unmarshal([]byte(fmt.Sprintf(`{"items":[%s],"next_cursor":%q}`, strings.Join(items, ","), next)), out)
		case "entities.get":
			if h.getErr != nil {
				return h.getErr
			}
			doc, ok := h.docs[a["id"].(string)]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			return json.Unmarshal([]byte(doc), out)
		case "db.exec":
			h.execs = append(h.execs, dbCall{fn, a["sql"].(string), a["args"].([]any)})
			return h.dbErr
		case "db.query":
			if h.dbErr != nil {
				return h.dbErr
			}
			cell, _ := json.Marshal(h.indexed)
			return json.Unmarshal([]byte(`{"rows":[[`+string(cell)+`]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (h *hookHost) resetCalls() { h.calls, h.execs = nil, nil }

func (h *hookHost) deletes() (ids []string) {
	for _, c := range h.execs {
		if strings.HasPrefix(c.sql, "DELETE FROM events") {
			ids = append(ids, c.args[0].(string))
		}
	}
	return ids
}

func (h *hookHost) upserts() (ids []string) {
	for _, c := range h.execs {
		if strings.HasPrefix(c.sql, "INSERT INTO events") {
			ids = append(ids, c.args[0].(string))
		}
	}
	return ids
}

func hookDoc(id, slug, status string) string {
	return `{"id":"` + id + `","schema_slug":"` + slug + `","slug":"` + id + `","name":"` + id + `","status":"` + status +
		`","attributes":{"start_at":"2026-07-01T19:00"}}`
}

func fire(t *testing.T, export string, payload any) {
	t.Helper()
	in, _ := json.Marshal(payload)
	if _, err := guest.Handle(export, in); err != nil {
		t.Fatalf("%s returned an error: %v", export, err)
	}
}

func entityEv(id, slug, status string) sdk.EntityEvent {
	return sdk.EntityEvent{ID: id, SchemaSlug: slug, Slug: id, Name: id, Status: status}
}

func TestHookUpsertsOnEntityEvents(t *testing.T) {
	h := &hookHost{settings: `{}`, docs: map[string]string{"e1": hookDoc("e1", "calendar-event", "published")}}
	h.install(t)
	for _, a := range []sdk.Action[sdk.EntityEvent]{sdk.EntityCreated, sdk.EntityUpdated, sdk.EntityPublished} {
		h.resetCalls()
		fire(t, a.Export(), entityEv("e1", "calendar-event", "published"))
		if got := h.upserts(); len(got) != 1 || got[0] != "e1" || len(h.execs) != 2 {
			t.Errorf("%s: execs = %+v", a.Name, h.execs)
		}
	}
}

func TestHookDeleteOnUnpublish(t *testing.T) {
	h := &hookHost{settings: `{}`, docs: map[string]string{"e1": hookDoc("e1", "event", "draft")}}
	h.install(t)
	fire(t, sdk.EntityUpdated.Export(), entityEv("e1", "event", "draft"))
	if got := h.deletes(); len(got) != 1 || got[0] != "e1" || len(h.upserts()) != 0 {
		t.Errorf("unpublish execs = %+v", h.execs)
	}
	// the deleted entity cannot be read any more: the hook must not need it
	h.resetCalls()
	delete(h.docs, "e1")
	fire(t, sdk.EntityDeleted.Export(), entityEv("e1", "event", ""))
	if got := h.deletes(); len(got) != 1 || got[0] != "e1" {
		t.Errorf("delete execs = %+v", h.execs)
	}
	for _, fn := range h.calls {
		if fn == "entities.get" {
			t.Error("entity.deleted read the entity")
		}
	}
}

func TestHookIgnoresOtherSchemas(t *testing.T) {
	h := &hookHost{settings: `{}`}
	h.install(t)
	for _, a := range []sdk.Action[sdk.EntityEvent]{sdk.EntityCreated, sdk.EntityUpdated, sdk.EntityPublished, sdk.EntityDeleted} {
		h.resetCalls()
		fire(t, a.Export(), entityEv("p1", "page", "published"))
		if len(h.calls) != 1 || h.calls[0] != "settings.get" {
			t.Errorf("%s: host calls = %v", a.Name, h.calls)
		}
	}
	// an explicit type excludes the others, new and old
	h.settings = `{"schema_slug":"gig"}`
	h.resetCalls()
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if len(h.calls) != 1 {
		t.Errorf("explicit slug: host calls = %v", h.calls)
	}
}

func TestReconcileCron(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs: map[string]string{
			"new": hookDoc("new", "calendar-event", "published"),
			"old": hookDoc("old", "event", "published"),
		},
		order:   []string{"new", "old"},
		indexed: map[string]string{"old": "2026-01-01T00:00:00Z", "stale": "2026-01-01T00:00:00Z"},
	}
	h.install(t)
	cron := func() { fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"}) }
	cron()
	if got := strings.Join(h.upserts(), ","); got != "new,old" {
		t.Errorf("upserts = %s", got)
	}
	if got := h.deletes(); len(got) != 1 || got[0] != "stale" {
		t.Errorf("deletes = %v", got)
	}
	// the index now matches: same upserts, nothing deleted
	h.resetCalls()
	h.indexed = map[string]string{"new": "x", "old": "x"}
	cron()
	if got := strings.Join(h.upserts(), ","); got != "new,old" || len(h.deletes()) != 0 {
		t.Errorf("second run: upserts = %s, deletes = %v", got, h.deletes())
	}
}

func TestReconcileMissingTypeStillPurges(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"new": hookDoc("new", "calendar-event", "published")},
		order:    []string{"new"},
		types:    []string{"calendar-event"}, // no "event" type on this site
		indexed:  map[string]string{"new": "x", "stale": "x"},
	}
	h.install(t)
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	if got := h.deletes(); len(got) != 1 || got[0] != "stale" {
		t.Errorf("deletes = %v", got)
	}
}

func TestReconcilePartialListingNeverDeletes(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"new": hookDoc("new", "calendar-event", "published")},
		order:    []string{"new"},
		indexed:  map[string]string{"stale": "x"},
		more:     true,
	}
	h.install(t)
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	lists := 0
	for _, fn := range h.calls {
		if fn == "entities.list" {
			lists++
		}
	}
	if lists != reconcilePages || len(h.deletes()) != 0 || len(h.upserts()) == 0 {
		t.Errorf("lists = %d, deletes = %v, upserts = %v", lists, h.deletes(), h.upserts())
	}
	// a failing listing deletes nothing either
	h.more, h.listErr = false, errors.New("down")
	h.resetCalls()
	fire(t, sdk.ImportCompleted.Export(), sdk.ImportEvent{Source: "x"})
	if len(h.deletes()) != 0 {
		t.Errorf("deleted after a failed listing: %v", h.deletes())
	}
}

func TestReconcileCollectsMapAddresses(t *testing.T) {
	h := &hookHost{
		settings: `{"show_map":true}`,
		docs: map[string]string{
			"e1": `{"id":"e1","schema_slug":"calendar-event","slug":"e1","name":"E","status":"published","attributes":{"start_at":"2026-07-01T19:00","venue_address":"1 High St","venue_city":"Bonn"}}`,
		},
		order:   []string{"e1"},
		indexed: map[string]string{"e1": "x"},
	}
	h.install(t)
	addrs, err := reconcileOnce(loadSettings(), clock().Add(reconcileBudget))
	if err != nil || len(addrs) != 1 || addrs[0] != "1 High St, Bonn" {
		t.Errorf("addrs = %v, err = %v", addrs, err)
	}
}

func TestHooksFailOpen(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"e1": hookDoc("e1", "calendar-event", "published")},
		order:    []string{"e1"},
		dbErr:    errors.New("no db"),
		listErr:  errors.New("no entities"),
		getErr:   errors.New("no entities"),
	}
	h.install(t)
	for _, a := range []sdk.Action[sdk.EntityEvent]{sdk.EntityCreated, sdk.EntityUpdated, sdk.EntityPublished, sdk.EntityDeleted} {
		fire(t, a.Export(), entityEv("e1", "calendar-event", "published"))
	}
	fire(t, sdk.ImportCompleted.Export(), sdk.ImportEvent{})
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{})
	// only the db is down: listing works, writes fail
	h.listErr, h.getErr = nil, nil
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{})
	// settings unreadable: defaults apply
	h.settings = `not json`
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
}
