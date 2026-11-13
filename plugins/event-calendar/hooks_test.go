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
	claimed  bool              // the hook reconcile slot is taken
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
			if args, _ := a["args"].([]any); len(args) > 0 && args[0] == reconcileKey {
				if h.claimed {
					return json.Unmarshal([]byte(`{"rows_affected":0}`), out)
				}
				h.claimed = true
				return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
			}
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

func TestUnreadyHookGeocodesAndThrottlesTheReconcile(t *testing.T) {
	doc := `{"id":"e1","schema_slug":"calendar-event","slug":"e1","name":"e1","status":"published",` +
		`"attributes":{"start_at":"2026-07-01T19:00","venue_address":"1 High St","venue_city":"Leeds"}}`
	h := &hookHost{settings: `{"show_map":true}`, docs: map[string]string{"e1": doc}, order: []string{"e1"}, unready: true}
	h.install(t)
	fetched := 0
	base := callHost
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "http.fetch":
			fetched++
			return json.Unmarshal([]byte(`{"status":200,"body":"[{\"lat\":\"53.8\",\"lon\":\"-1.5\"}]"}`), out)
		case "db.exec":
			if a, _ := args.(map[string]any)["args"].([]any); len(a) > 0 && a[0] == throttleKey {
				return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
			}
		}
		return base(fn, args, out)
	}
	t.Cleanup(func() { callHost = base })
	lists := func() (n int) {
		for _, fn := range h.calls {
			if fn == "entities.list" {
				n++
			}
		}
		return
	}
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if fetched != 1 {
		t.Errorf("lookups on an unready index = %d, want 1", fetched)
	}
	if lists() == 0 {
		t.Error("the first hook did not reconcile")
	}
	// a second save inside the window (the index is still unready) must not reconcile again
	h.calls = nil
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if lists() != 0 {
		t.Errorf("a second hook inside the window listed %d times", lists())
	}
}

func TestHookSkipsTheReconcileWhenTheReadyCheckFails(t *testing.T) {
	h := &hookHost{settings: `{}`, docs: map[string]string{"e1": hookDoc("e1", "calendar-event", "published")}, order: []string{"e1"}, unready: true}
	h.install(t)
	base := callHost
	callHost = func(fn string, args, out any) error {
		if a, _ := args.(map[string]any); fn == "db.query" && strings.Contains(a["sql"].(string), "FROM index_state") {
			return errors.New("no db")
		}
		return base(fn, args, out)
	}
	t.Cleanup(func() { callHost = base })
	fire(t, sdk.EntityPublished.Export(), entityEv("e1", "calendar-event", "published"))
	if slices.Contains(h.calls, "entities.list") {
		t.Error("reconciled although the ready check failed")
	}
}

func TestReconcileKeepsRowsWrittenAfterItStarted(t *testing.T) {
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"listed": hookDoc("listed", "calendar-event", "published")},
		order:    []string{"listed"},
		indexed: map[string]string{
			"gone": "2000-01-01T00:00:00.123456+00:00", // indexed long ago, no longer listed
			"late": "2999-01-01T00:00:00.123456+00:00", // a hook wrote it after the listing was read
		},
	}
	h.install(t)
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	if got := strings.Join(h.deletes(), ","); got != "gone" {
		t.Errorf("deleted = %q, want only gone", got)
	}
}

// occurrenceSets returns the dates sent per event by the occurrence statements.
func (h *hookHost) occurrenceSets() map[string][]string {
	out := map[string][]string{}
	for _, c := range h.execs {
		if !strings.HasPrefix(c.sql, "WITH d AS (DELETE FROM occurrences") {
			continue
		}
		var rows []struct{ S string }
		_ = json.Unmarshal([]byte(c.args[1].(string)), &rows)
		var starts []string
		for _, r := range rows {
			starts = append(starts, r.S)
		}
		out[c.args[0].(string)] = starts
	}
	return out
}

func weeklyHookDoc(id string) string {
	return `{"id":"` + id + `","schema_slug":"calendar-event","slug":"` + id + `","name":"` + id + `","status":"published",` +
		`"attributes":{"start_at":"2026-01-06T18:00","time_zone":"UTC","repeat":"weekly","repeat_every":1}}`
}

func TestRollingWindowCron(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	t0 := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	t1 := t0.AddDate(0, 0, 60)
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"w": weeklyHookDoc("w")},
		order:    []string{"w"},
	}
	h.install(t)
	runAt := func(at time.Time) []string {
		t.Helper()
		now = func() time.Time { return at }
		h.resetCalls()
		fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
		return h.occurrenceSets()["w"]
	}
	first := runAt(t0)
	if len(first) == 0 || first[len(first)-1] > "2027-12-01T18:00:00Z" || first[len(first)-1] < "2027-11-24T18:00:00Z" {
		t.Fatalf("first window ends at %v", first[len(first)-1])
	}
	// 60 days later the window reaches 60 days further and still keeps the
	// rows of the 90 days before it.
	second := runAt(t1)
	last := second[len(second)-1]
	if last <= first[len(first)-1] || last > "2028-01-31T18:00:00Z" || last < "2028-01-24T18:00:00Z" {
		t.Errorf("second window ends at %v", last)
	}
	if second[0] < "2026-05-02T00:00:00Z" || second[0] > "2026-05-09T00:00:00Z" {
		t.Errorf("second window starts at %v, 90 days before is 2 May", second[0])
	}
	// Running again at the same time writes the same set.
	if again := runAt(t1); strings.Join(again, ",") != strings.Join(second, ",") {
		t.Errorf("a repeated run changed the set")
	}
}

// A reconcile that runs out of time leaves what it wrote and touches nothing
// of the events it did not reach.
func TestRollingWindowCronPartialRunLeavesRowsIntact(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }
	h := &hookHost{
		settings: `{}`,
		docs:     map[string]string{"a": weeklyHookDoc("a"), "b": weeklyHookDoc("b")},
		order:    []string{"a", "b"},
	}
	h.install(t)
	start := time.Now()
	elapsed := time.Duration(0)
	clock = func() time.Time { return start.Add(elapsed) }
	t.Cleanup(func() { clock = time.Now })
	base := callHost
	callHost = func(fn string, args, out any) error {
		err := base(fn, args, out)
		if fn == "db.exec" && strings.HasPrefix(args.(map[string]any)["sql"].(string), "WITH d AS (DELETE FROM occurrences") {
			elapsed = time.Minute // the budget is gone after the first event
		}
		return err
	}
	t.Cleanup(func() { callHost = base })
	fire(t, guest.CronExport("reconcile"), sdk.CronEvent{Name: "reconcile"})
	sets := h.occurrenceSets()
	if len(sets["a"]) == 0 {
		t.Errorf("the reached event was not written: %v", sets)
	}
	if _, ok := sets["b"]; ok {
		t.Errorf("an event that was not reached was touched")
	}
	for _, c := range h.execs {
		if c.args[0] == "b" || strings.Contains(c.sql, "DELETE FROM events WHERE entity_id") {
			t.Errorf("unexpected statement for an unreached event or a delete: %s %v", c.sql, c.args[0])
		}
	}
}

// submissionHost fakes the host for the submission hooks: entities.get,
// settings, the submissions table and the mail call.
type submissionHost struct {
	doc      string
	notify   string
	dbErr    error
	mailErr  error
	rows     string // the unnotified rows, as the jsonb cell
	execs    []dbCall
	mails    []map[string]any
	notified string // the ids markNotified was called with
}

func (h *submissionHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(`{"notify_email":"`+h.notify+`"}`), out)
		case "entities.get":
			return json.Unmarshal([]byte(h.doc), out)
		case "mail.send":
			h.mails = append(h.mails, a)
			return h.mailErr
		case "db.exec":
			sql := a["sql"].(string)
			h.execs = append(h.execs, dbCall{fn, sql, a["args"].([]any)})
			if h.dbErr != nil {
				return h.dbErr
			}
			if strings.HasPrefix(sql, "UPDATE submissions SET notified") {
				h.notified = a["args"].([]any)[0].(string)
			}
			if out != nil {
				return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
			}
			return nil
		case "db.query":
			if h.dbErr != nil {
				return h.dbErr
			}
			if strings.Contains(a["sql"].(string), "NOT notified") {
				return json.Unmarshal([]byte(`{"rows":[[`+h.rows+`]]}`), out)
			}
			return json.Unmarshal([]byte(`{"rows":[[{}]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (h *submissionHost) statusUpdates() (out []dbCall) {
	for _, c := range h.execs {
		if strings.HasPrefix(c.sql, "UPDATE submissions SET status") {
			out = append(out, c)
		}
	}
	return out
}

func submissionDoc(status, kind, uid string) string {
	return `{"id":"s1","schema_slug":"calendar-event","slug":"s1","name":"s1","status":"` + status +
		`","attributes":{"start_at":"2027-02-20T20:00","source_kind":"` + kind + `","source_uid":"` + uid + `"}}`
}

func TestSubmissionMarkedWhenPublished(t *testing.T) {
	for _, a := range []sdk.Action[sdk.EntityEvent]{sdk.EntityCreated, sdk.EntityPublished} {
		t.Run(a.Name, func(t *testing.T) {
			h := &submissionHost{doc: submissionDoc("published", "submission", "sub-42")}
			h.install(t)
			fire(t, a.Export(), entityEv("s1", "calendar-event", "published"))
			got := h.statusUpdates()
			if len(got) != 1 || got[0].args[0] != int64(42) {
				t.Fatalf("status updates = %+v", h.execs)
			}
			if got[0].args[1] != "published" || got[0].args[2] != "pending" || got[0].args[3] != "approved" || strings.Contains(got[0].sql, "rejected") {
				t.Errorf("update = %+v", got[0])
			}
		})
	}
	ignored := map[string]string{
		"not a submission": submissionDoc("published", "ical", "sub-42"),
		"draft":            submissionDoc("draft", "submission", "sub-42"),
		"odd uid":          submissionDoc("published", "submission", "sub-4x"),
		"uid with suffix":  submissionDoc("published", "submission", "sub-42/yoga"),
		"no attributes":    `{"id":"s1","schema_slug":"calendar-event","slug":"s1","name":"s1","status":"published"}`,
	}
	for name, doc := range ignored {
		t.Run(name, func(t *testing.T) {
			h := &submissionHost{doc: doc}
			h.install(t)
			fire(t, sdk.EntityPublished.Export(), entityEv("s1", "calendar-event", "published"))
			if got := h.statusUpdates(); len(got) != 0 {
				t.Errorf("status updates = %+v", got)
			}
		})
	}
	t.Run("db error is ignored", func(t *testing.T) {
		h := &submissionHost{doc: submissionDoc("published", "submission", "sub-42"), dbErr: errors.New("permission denied for table submissions")}
		h.install(t)
		fire(t, sdk.EntityPublished.Export(), entityEv("s1", "calendar-event", "published"))
	})
}

func TestPruneCron(t *testing.T) {
	rows := `[{"id":1,"title":"Open mic","email":"a@example.org"},{"id":2,"title":"Quiz","email":"b@example.org"}]`
	prune := func() { fire(t, guest.CronExport("prune"), sdk.CronEvent{Name: "prune"}) }

	t.Run("prunes and sends one digest", func(t *testing.T) {
		h := &submissionHost{notify: "owner@example.org", rows: rows}
		h.install(t)
		prune()
		if len(h.execs) < 1 || !strings.HasPrefix(h.execs[0].sql, "DELETE FROM submissions") || len(h.execs[0].args) != 2 {
			t.Fatalf("execs = %+v", h.execs)
		}
		if len(h.mails) != 1 {
			t.Fatalf("mails = %v", h.mails)
		}
		text := h.mails[0]["text"].(string)
		if !strings.Contains(text, "- Open mic") || !strings.Contains(text, "- Quiz") || strings.Contains(text, "@") {
			t.Errorf("digest = %q", text)
		}
		if h.notified != "[1,2]" {
			t.Errorf("marked %q", h.notified)
		}
	})
	t.Run("mail failure keeps the rows", func(t *testing.T) {
		h := &submissionHost{notify: "owner@example.org", rows: rows, mailErr: errors.New("smtp down")}
		h.install(t)
		prune()
		if h.notified != "" {
			t.Errorf("marked %q after a failed mail", h.notified)
		}
	})
	t.Run("no address, no mail", func(t *testing.T) {
		h := &submissionHost{rows: rows}
		h.install(t)
		prune()
		if len(h.mails) != 0 || len(h.execs) != 1 {
			t.Errorf("mails %v execs %+v", h.mails, h.execs)
		}
	})
	t.Run("nothing waiting", func(t *testing.T) {
		h := &submissionHost{notify: "owner@example.org", rows: `[]`}
		h.install(t)
		prune()
		if len(h.mails) != 0 {
			t.Errorf("mails %v", h.mails)
		}
	})
	t.Run("titles are capped", func(t *testing.T) {
		var list []string
		for i := 1; i <= 25; i++ {
			list = append(list, fmt.Sprintf(`{"id":%d,"title":"T%d"}`, i, i))
		}
		h := &submissionHost{notify: "owner@example.org", rows: "[" + strings.Join(list, ",") + "]"}
		h.install(t)
		prune()
		text := h.mails[0]["text"].(string)
		if !strings.Contains(text, "- T20\n") || strings.Contains(text, "- T21\n") || !strings.Contains(text, "and 5 more") {
			t.Errorf("digest = %q", text)
		}
	})
	t.Run("db down", func(t *testing.T) {
		h := &submissionHost{notify: "owner@example.org", dbErr: errors.New("down")}
		h.install(t)
		prune()
		if len(h.mails) != 0 {
			t.Errorf("mails %v", h.mails)
		}
	})
}
