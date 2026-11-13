package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
	"time"

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
	unready  bool              // index_state has no row yet
	failID   string            // db.exec fails for the statements of this entity id
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
			if h.failID != "" && a["args"].([]any)[0] == h.failID {
				return errors.New("check constraint")
			}
			return h.dbErr
		case "db.query":
			if h.dbErr != nil {
				return h.dbErr
			}
			if strings.Contains(a["sql"].(string), "EXISTS (SELECT 1 FROM index_state)") {
				return json.Unmarshal([]byte(fmt.Sprintf(`{"rows":[[%t]]}`, !h.unready)), out)
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
		if strings.Contains(c.sql, "DELETE FROM events WHERE entity_id") {
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

func TestReconcileMarksIndexAndGuardsAgainstDeletes(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"new": hookDoc("new", "calendar-event", "published")},
		order:    []string{"new"},
	}
	h.install(t)
	marks := func() (n int) {
		for _, c := range h.execs {
			if strings.Contains(c.sql, "INTO index_state") {
				n++
			}
		}
		return
	}
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	if marks() != 1 {
		t.Errorf("a complete reconcile wrote %d markers", marks())
	}
	// the reconcile's upsert is skipped for an event a hook deleted after the run began; hooks' upserts are not
	if c := h.execs[0]; !strings.Contains(c.sql, "deleted_events") || c.args[24] == nil {
		t.Errorf("reconcile upsert has no delete guard: %v", c.args)
	}
	h.resetCalls()
	fire(t, sdk.EntityPublished.Export(), entityEv("new", "calendar-event", "published"))
	if c := h.execs[0]; c.args[24] != nil {
		t.Errorf("hook upsert is guarded: %v", c.args[24])
	}
	// an incomplete or failed run leaves the index untrusted
	h.resetCalls()
	h.more = true
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	h.more, h.listErr = false, errors.New("down")
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	if marks() != 0 {
		t.Errorf("marker written by an incomplete run")
	}
}

func TestReconcileSkipsARowThatFailsToWrite(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs: map[string]string{
			"bad":  hookDoc("bad", "calendar-event", "published"),
			"good": hookDoc("good", "calendar-event", "published"),
		},
		order:   []string{"bad", "good"},
		failID:  "bad",
		indexed: map[string]string{"stale": "x"},
	}
	h.install(t)
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	if got := strings.Join(h.upserts(), ","); got != "bad,good" {
		t.Errorf("upserts tried = %s, the rows after the failing one must still be written", got)
	}
	if len(h.deletes()) != 0 {
		t.Errorf("deleted after a failed write: %v", h.deletes())
	}
	for _, c := range h.execs {
		if strings.Contains(c.sql, "INTO index_state") {
			t.Error("marker written by a run with a failed row")
		}
	}
}

func TestHookGeocodesTheVenueOfASavedEvent(t *testing.T) {
	doc := `{"id":"e1","schema_slug":"calendar-event","slug":"e1","name":"e1","status":"published",` +
		`"attributes":{"start_at":"2026-07-01T19:00","venue_address":"1 High St","venue_city":"Leeds"}}`
	var fetched []any
	h := &hookHost{settings: `{"show_map":true}`, docs: map[string]string{"e1": doc}}
	h.install(t)
	base := callHost
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "http.fetch":
			fetched = append(fetched, args)
			return json.Unmarshal([]byte(`{"status":200,"body":"[{\"lat\":\"53.8\",\"lon\":\"-1.5\"}]"}`), out)
		case "db.exec":
			if a := args.(map[string]any)["args"].([]any); a[0] == throttleKey {
				return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
			}
		}
		return base(fn, args, out)
	}
	t.Cleanup(func() { callHost = base })
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if len(fetched) != 1 {
		t.Fatalf("lookups on publish = %d, want 1", len(fetched))
	}
	h.settings = `{}` // Show map off: no lookup
	fetched = nil
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if len(fetched) != 0 {
		t.Errorf("lookups with Show map off = %d", len(fetched))
	}
}

// The hook may run 5 s in all: the lookup must end before that however long the
// reindex before it took.
func TestHookGeocodeEndsWithinTheHookTimeout(t *testing.T) {
	doc := `{"id":"e1","schema_slug":"calendar-event","slug":"e1","name":"e1","status":"published",` +
		`"attributes":{"start_at":"2026-07-01T19:00","venue_address":"1 High St","venue_city":"Leeds"}}`
	h := &hookHost{settings: `{"show_map":true}`, docs: map[string]string{"e1": doc}}
	h.install(t)
	start := time.Now()
	elapsed := time.Duration(0)
	clock = func() time.Time { return start.Add(elapsed) }
	t.Cleanup(func() { clock = time.Now })
	var worst time.Duration
	base := callHost
	callHost = func(fn string, args, out any) error {
		elapsed += 300 * time.Millisecond // every host call is slow
		if fn == "http.fetch" {
			timeout := time.Duration(args.(map[string]any)["timeout_ms"].(int)) * time.Millisecond
			worst = elapsed + timeout
			return json.Unmarshal([]byte(`{"status":200,"body":"[]"}`), out)
		}
		if fn == "db.exec" {
			if a := args.(map[string]any)["args"].([]any); a[0] == throttleKey {
				return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
			}
		}
		return base(fn, args, out)
	}
	t.Cleanup(func() { callHost = base })
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if worst == 0 {
		t.Fatal("no lookup was made")
	}
	if worst > 4*time.Second {
		t.Errorf("the lookup may end %v after the hook started, the hook timeout is 5 s", worst)
	}
}

func TestFirstEntityHookReconcilesAnUnreadyIndex(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs: map[string]string{
			"e1": hookDoc("e1", "calendar-event", "published"),
			"e2": hookDoc("e2", "calendar-event", "published"),
		},
		order:   []string{"e1", "e2"},
		unready: true,
	}
	h.install(t)
	marks := func() (n int) {
		for _, c := range h.execs {
			if strings.Contains(c.sql, "INTO index_state") {
				n++
			}
		}
		return
	}
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if marks() != 1 || !slices.Contains(h.upserts(), "e2") {
		t.Errorf("the first hook did not reconcile: marks = %d, upserts = %v", marks(), h.upserts())
	}
	// once ready, a hook indexes only its own entity
	h.unready = false
	h.resetCalls()
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if marks() != 0 || len(h.upserts()) != 1 {
		t.Errorf("ready index: marks = %d, upserts = %v", marks(), h.upserts())
	}
}
