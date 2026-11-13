package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

type dbCall struct {
	fn, sql string
	args    []any
}

// indexHost answers entities.get from docs keyed by id and records db calls.
type indexHost struct {
	docs    map[string]string
	dbErr   error
	queryTo string // raw db.query result
	calls   []dbCall
}

func (h *indexHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		a := args.(map[string]any)
		switch fn {
		case "entities.get":
			doc, ok := h.docs[a["id"].(string)]
			if !ok {
				return &sdk.HostError{Code: sdk.ErrNotFound}
			}
			return json.Unmarshal([]byte(doc), out)
		case "db.exec", "db.query":
			sql, _ := a["sql"].(string)
			h.calls = append(h.calls, dbCall{fn, sql, a["args"].([]any)})
			if h.dbErr != nil {
				return h.dbErr
			}
			if fn == "db.query" {
				return json.Unmarshal([]byte(h.queryTo), out)
			}
			return nil
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (h *indexHost) execs() []dbCall {
	var out []dbCall
	for _, c := range h.calls {
		if c.fn == "db.exec" {
			out = append(out, c)
		}
	}
	return out
}

func (h *indexHost) assertSingleStatements(t *testing.T) {
	t.Helper()
	for _, c := range h.calls {
		sql := strings.TrimSpace(c.sql)
		if strings.Contains(strings.TrimSuffix(sql, ";"), ";") {
			t.Errorf("more than one statement: %s", sql)
		}
		if !strings.HasPrefix(sql, "INSERT") && !strings.HasPrefix(sql, "WITH") && !strings.HasPrefix(sql, "DELETE") && !strings.HasPrefix(sql, "SELECT") {
			t.Errorf("unexpected statement: %s", sql)
		}
	}
}

const idxDoc = `{"id":"e1","schema_slug":"calendar-event","slug":"gala","name":"Gala","status":"published",
 "attributes":{"start_at":"2026-07-01T19:00","end_at":"2026-07-01T21:00","time_zone":"Europe/Berlin","venue":["v1"],"category":["c1"],"price_amount":12.5}}`

func TestIndexUpsertOnEntityEvents(t *testing.T) {
	h := &indexHost{docs: map[string]string{
		"e1": idxDoc,
		"v1": `{"name":"Blue Room","slug":"blue-room","status":"published","attributes":{"city":"Bonn","address":"1 Main St","latitude":50.7,"longitude":7.1}}`,
		"c1": `{"name":"Music","slug":"music","status":"published"}`,
	}}
	h.install(t)
	ok, err := reindexEntity("e1", settings{TimeZone: "UTC"})
	if !ok || err != nil {
		t.Fatalf("reindex = %v, %v", ok, err)
	}
	ex := h.execs()
	if len(ex) != 2 || !strings.HasPrefix(ex[0].sql, "INSERT INTO events") || !strings.Contains(ex[0].sql, "ON CONFLICT (entity_id) DO UPDATE") ||
		!strings.HasPrefix(ex[1].sql, "WITH d AS (DELETE FROM occurrences") {
		t.Fatalf("statements = %+v", ex)
	}
	// 19:00 and 21:00 in Berlin (UTC+2 in July)
	if ex[0].args[5] != "2026-07-01T17:00:00Z" || ex[0].args[6] != "2026-07-01T19:00:00Z" {
		t.Errorf("event times = %v %v", ex[0].args[5], ex[0].args[6])
	}
	if ex[0].args[12] != "Blue Room" || ex[0].args[13] != "Bonn" || ex[0].args[15] != `["Music"]` || ex[0].args[18] != 12.5 {
		t.Errorf("event args = %v", ex[0].args)
	}
	// guard stays $25; url, venue slug, address, lat, lon, category slugs follow it
	if ex[0].args[24] != nil || ex[0].args[26] != "blue-room" || ex[0].args[27] != "1 Main St, Bonn" ||
		ex[0].args[28] != 50.7 || ex[0].args[29] != 7.1 || ex[0].args[30] != `["music"]` {
		t.Errorf("view args = %v", ex[0].args[23:])
	}
	if len(ex[0].args) != 31 || strings.Count(ex[0].sql, "$31") != 1 {
		t.Errorf("args = %d", len(ex[0].args))
	}
	if ex[1].args[0] != "e1" || ex[1].args[1] != "2026-07-01T17:00:00Z" || ex[1].args[2] != "2026-07-01T19:00:00Z" {
		t.Errorf("occurrence args = %v", ex[1].args)
	}
	h.assertSingleStatements(t)
}

func TestIndexAllDayEndsAtNextLocalMidnight(t *testing.T) {
	h := &indexHost{docs: map[string]string{"e2": `{"id":"e2","schema_slug":"event","slug":"fair","name":"Fair","status":"published",
		"attributes":{"start_at":"2026-03-07T10:00","end_at":"2026-03-08","all_day":true}}`}}
	h.install(t)
	if ok, err := reindexEntity("e2", settings{TimeZone: "Europe/Berlin"}); !ok || err != nil {
		t.Fatalf("reindex = %v, %v", ok, err)
	}
	ex := h.execs()
	// 7 March 00:00 Berlin = 6 March 23:00 UTC; ends 9 March 00:00 Berlin
	if ex[0].args[5] != "2026-03-06T23:00:00Z" || ex[0].args[6] != "2026-03-08T23:00:00Z" {
		t.Errorf("times = %v %v", ex[0].args[5], ex[0].args[6])
	}
}

func TestIndexDeleteOnUnpublish(t *testing.T) {
	h := &indexHost{docs: map[string]string{
		"d1": `{"id":"d1","schema_slug":"calendar-event","slug":"x","name":"X","status":"draft","attributes":{"start_at":"2026-07-01"}}`,
		"p1": `{"id":"p1","schema_slug":"post","slug":"x","name":"X","status":"published","attributes":{"start_at":"2026-07-01"}}`,
		"n1": `{"id":"n1","schema_slug":"event","slug":"x","name":"X","status":"published","attributes":{}}`,
	}}
	h.install(t)
	for _, id := range []string{"d1", "p1", "n1", "gone"} {
		h.calls = nil
		ok, err := reindexEntity(id, settings{})
		ex := h.execs()
		if ok || err != nil || len(ex) != 1 || !strings.Contains(ex[0].sql, "DELETE FROM events WHERE entity_id = $1") || ex[0].args[0] != id {
			t.Errorf("%s: ok=%v err=%v calls=%+v", id, ok, err, ex)
		}
	}
	h.assertSingleStatements(t)
}

func TestIndexHostErrorFailsOpen(t *testing.T) {
	h := &indexHost{docs: map[string]string{"e1": idxDoc}, dbErr: errors.New("no db")}
	h.install(t)
	if ok, err := reindexEntity("e1", settings{}); ok || err == nil {
		t.Errorf("reindex = %v, %v", ok, err)
	}
	if err := deleteEvent("e1"); err == nil {
		t.Error("delete swallowed the error")
	}
	if _, err := indexedIDs(); err == nil {
		t.Error("indexedIDs swallowed the error")
	}
	if _, err := queryEvents(indexQuery{Upcoming: true, At: time.Now()}); err == nil {
		t.Error("queryEvents swallowed the error")
	}
	callHost = func(string, any, any) error { return errors.New("host down") }
	if ok, err := reindexEntity("e1", settings{}); ok || err == nil {
		t.Errorf("reindex on entities error = %v, %v", ok, err)
	}
}

func TestIndexedIDs(t *testing.T) {
	h := &indexHost{queryTo: `{"rows":[[{"a":"2026-01-01T00:00:00Z","b":"2026-01-02T00:00:00Z"}]]}`}
	h.install(t)
	m, err := indexedIDs()
	if err != nil || len(m) != 2 || m["a"] == "" {
		t.Fatalf("ids = %v, %v", m, err)
	}
	h.queryTo = `{"rows":[["{}"]]}` // jsonb delivered as a string
	if m, err = indexedIDs(); err != nil || len(m) != 0 {
		t.Fatalf("ids = %v, %v", m, err)
	}
}

func TestIndexQueryEvents(t *testing.T) {
	rows := `[{"id":"e1","schema_slug":"calendar-event","slug":"gala","locale":"","name":"Gala","starts_at":"2026-07-01T17:00:00+00:00",
		"ends_at":"2026-07-01T19:00:00+00:00","all_day":false,"time_zone":"Europe/Berlin","event_status":"cancelled","venue_name":"Hall",
		"category_ids":["c1"],"price_amount":null,"updated_at":"2026-05-01T10:00:00.123456+00:00"},
		{"id":"e2","name":"Fair","starts_at":"2026-03-06T23:00:00+00:00","ends_at":"2026-03-08T23:00:00+00:00","all_day":true}]`
	h := &indexHost{queryTo: `{"rows":[[` + rows + `]]}`}
	h.install(t)
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	evs, err := queryEvents(indexQuery{Upcoming: true, At: at, Language: "de", Limit: 9999, Slug: "gala", Loc: mustLoc(t, "Europe/Berlin")})
	if err != nil || len(evs) != 2 {
		t.Fatalf("events = %+v, %v", evs, err)
	}
	e := evs[0]
	if e.ID != "e1" || e.Name != "Gala" || !e.Start.Equal(time.Date(2026, 7, 1, 17, 0, 0, 0, time.UTC)) || !e.HasEnd ||
		e.EventStatus != "cancelled" || e.Venue != "Hall" || fmt.Sprint(e.CategoryIDs) != "[c1]" || e.PriceAmount != nil || e.Updated.IsZero() || e.URL != "" {
		t.Errorf("event = %+v", e)
	}
	// all-day: 7 and 8 March in Berlin; ends at the next local midnight again
	f := evs[1]
	loc := mustLoc(t, "Europe/Berlin")
	if !f.AllDay || !f.HasEnd || f.Start.In(loc).Day() != 7 || f.End.In(loc).Day() != 8 || !f.endsAt(loc).Equal(time.Date(2026, 3, 8, 23, 0, 0, 0, time.UTC)) {
		t.Errorf("all-day = %+v", f)
	}
	c := h.calls[0]
	if !strings.Contains(c.sql, "o.ends_at > $1") || c.args[0] != "2026-06-01T12:00:00Z" || c.args[1] != "de" || c.args[2] != "gala" || c.args[3] != maxIndexRows {
		t.Errorf("query = %s %v", c.sql, c.args)
	}
	if _, err := queryEvents(indexQuery{At: at}); err != nil || !strings.Contains(h.calls[1].sql, "o.ends_at <= $1") ||
		!strings.Contains(h.calls[1].sql, "o.starts_at DESC") {
		t.Errorf("past query = %s", h.calls[1].sql)
	}
	h.assertSingleStatements(t)
}

func TestIndexNotTrustedBeforeFirstReconcile(t *testing.T) {
	h := &indexHost{queryTo: `{"rows":[[null]]}`} // the query yields NULL until index_state has a row
	h.install(t)
	if _, err := queryEvents(indexQuery{Upcoming: true, At: time.Now()}); !errors.Is(err, errIndexNotReady) {
		t.Errorf("err = %v", err)
	}
	h.queryTo = `{"rows":[[[]]]}`
	if evs, err := queryEvents(indexQuery{Upcoming: true, At: time.Now()}); err != nil || len(evs) != 0 {
		t.Errorf("empty reconciled index: %v, %v", evs, err)
	}
}

func TestIndexQueryRange(t *testing.T) {
	rows := `[{"id":"e1","name":"Gala","slug":"gala","url":"/event/gala","starts_at":"2026-07-01T17:00:00+00:00",
		"ends_at":"2026-07-01T19:00:00+00:00","all_day":false,"venue_name":"Hall","venue_slug":"hall","lat":50.5,"lon":null,
		"category_names":["Music"],"price":"","price_amount":0,"featured":true},
		{"id":"e2","name":"Fair","starts_at":"2026-03-06T23:00:00+00:00","ends_at":"2026-03-08T23:00:00+00:00","all_day":true}]`
	h := &indexHost{queryTo: `{"rows":[[` + rows + `]]}`}
	h.install(t)
	from := time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC)
	to := from.AddDate(0, 0, 31)
	got, err := queryRange(rangeQuery{From: from, To: to, Q: `50%_\ "off"; DROP`, Category: "Music", Venue: "hall", City: "Bonn", Price: "free", Slug: "gala", Language: "de", Limit: 9999})
	if err != nil || len(got) != 2 {
		t.Fatalf("rows = %+v, %v", got, err)
	}
	if g := got[0]; g.URL != "/event/gala" || g.VenueSlug != "hall" || g.Lat == nil || *g.Lat != 50.5 || g.Lon != nil ||
		g.PriceAmount == nil || !g.Featured || g.CategoryNames[0] != "Music" || !g.StartsAt.Equal(time.Date(2026, 7, 1, 17, 0, 0, 0, time.UTC)) {
		t.Errorf("row = %+v", g)
	}
	c := h.calls[0]
	// $1 from, $2 to: a row overlaps when it starts before to and ends after from
	if !strings.Contains(c.sql, "o.starts_at < $2::timestamptz AND o.ends_at > $1::timestamptz") {
		t.Errorf("no overlap test: %s", c.sql)
	}
	want := []any{"2026-07-01T00:00:00Z", "2026-08-01T00:00:00Z", `50\%\_\\ "off"; DROP`, "Music", "hall", "Bonn", "free", "gala", "de", maxIndexRows}
	if fmt.Sprint(c.args) != fmt.Sprint(want) {
		t.Errorf("args = %v, want %v", c.args, want)
	}
	if strings.Contains(c.sql, "DROP") || strings.Contains(c.sql, "Music") {
		t.Errorf("input in SQL: %s", c.sql)
	}
	h.assertSingleStatements(t)

	h.queryTo = `{"rows":[[null]]}`
	if _, err := queryRange(rangeQuery{From: from, To: to}); !errors.Is(err, errIndexNotReady) {
		t.Errorf("not ready: err = %v", err)
	}
	h.dbErr = errors.New("no db")
	if _, err := queryRange(rangeQuery{From: from, To: to}); err == nil || errors.Is(err, errIndexNotReady) {
		t.Errorf("db error = %v", err)
	}
}

func TestEscapeLike(t *testing.T) {
	for in, want := range map[string]string{"": "", "plain": "plain", "a%b_c": `a\%b\_c`, `x\y`: `x\\y`} {
		if got := escapeLike(in); got != want {
			t.Errorf("escapeLike(%q) = %q, want %q", in, got, want)
		}
	}
}
