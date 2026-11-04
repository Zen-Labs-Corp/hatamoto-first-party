package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"sort"
	"strings"
	"testing"
	"time"
)

type fetchCall struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

type fetchRoute struct {
	prefix string
	fn     func(fetchCall) (int, string, error)
}

type kvEntry struct {
	Value json.RawMessage
	TTL   int
}

// fake is an in-memory host: settings, kv, http.fetch, a small db for the
// item and source_status tables, and the log.
type fake struct {
	t        *testing.T
	settings string
	kv       map[string]kvEntry
	kvCalls  []string
	routes   []fetchRoute
	fetches  []fetchCall
	items    []item
	status   map[string]sourceStatus
	logs     []string
	failDB   string // any db statement containing this text fails
	clock    time.Time
}

// useFake installs a fresh fake as the host and a fixed clock.
func useFake(t *testing.T) *fake {
	t.Helper()
	f := &fake{
		t:        t,
		settings: `{}`,
		kv:       map[string]kvEntry{},
		status:   map[string]sourceStatus{},
		clock:    time.Date(2026, 1, 2, 12, 0, 0, 0, time.UTC),
	}
	oldHost, oldNow := callHost, now
	callHost = f.call
	now = func() time.Time { return f.clock }
	t.Cleanup(func() { callHost, now = oldHost, oldNow })
	return f
}

// on answers fetches whose URL starts with prefix.
func (f *fake) on(prefix string, status int, body string) {
	f.onFunc(prefix, func(fetchCall) (int, string, error) { return status, body, nil })
}

func (f *fake) onFunc(prefix string, fn func(fetchCall) (int, string, error)) {
	f.routes = append([]fetchRoute{{prefix, fn}}, f.routes...)
}

func (f *fake) fail(prefix string, err error) {
	f.onFunc(prefix, func(fetchCall) (int, string, error) { return 0, "", err })
}

func (f *fake) fetchCount() int { return len(f.fetches) }

func (f *fake) rows(source string) []item {
	var out []item
	for _, it := range f.items {
		if it.Source == source {
			out = append(out, it)
		}
	}
	return out
}

func (f *fake) call(fn string, args, out any) error {
	raw, _ := json.Marshal(args)
	var a map[string]json.RawMessage
	_ = json.Unmarshal(raw, &a)
	str := func(k string) string {
		var s string
		_ = json.Unmarshal(a[k], &s)
		return s
	}
	reply := func(v any) error {
		if out == nil {
			return nil
		}
		b, _ := json.Marshal(v)
		return json.Unmarshal(b, out)
	}
	switch fn {
	case "settings.get":
		return json.Unmarshal([]byte(f.settings), out)
	case "log":
		f.logs = append(f.logs, str("level")+": "+str("message"))
		return nil
	case "kv.get":
		f.kvCalls = append(f.kvCalls, "get "+str("key"))
		e, ok := f.kv[str("key")]
		return reply(map[string]any{"found": ok, "value": e.Value})
	case "kv.set":
		f.kvCalls = append(f.kvCalls, "set "+str("key"))
		var ttl int
		_ = json.Unmarshal(a["ttl_seconds"], &ttl)
		f.kv[str("key")] = kvEntry{Value: a["value"], TTL: ttl}
		return nil
	case "kv.delete":
		f.kvCalls = append(f.kvCalls, "delete "+str("key"))
		delete(f.kv, str("key"))
		return nil
	case "http.fetch":
		c := fetchCall{Method: str("method"), URL: str("url"), Body: str("body")}
		_ = json.Unmarshal(a["headers"], &c.Headers)
		f.fetches = append(f.fetches, c)
		for _, r := range f.routes {
			if strings.HasPrefix(c.URL, r.prefix) {
				status, body, err := r.fn(c)
				if err != nil {
					return err
				}
				return reply(map[string]any{"status": status, "body": body})
			}
		}
		return fmt.Errorf("fake: no route for %s", c.URL)
	case "db.exec", "db.query":
		return f.db(fn, str("sql"), a["args"], reply)
	}
	f.t.Fatalf("fake: unexpected host call %q", fn)
	return nil
}

func (f *fake) db(fn, sql string, rawArgs json.RawMessage, reply func(any) error) error {
	if f.failDB != "" && strings.Contains(sql, f.failDB) {
		return errors.New("fake: db failure")
	}
	var args []json.RawMessage
	_ = json.Unmarshal(rawArgs, &args)
	argStr := func(i int) string {
		var s string
		_ = json.Unmarshal(args[i], &s)
		return s
	}
	cell := func(v any) error {
		b, _ := json.Marshal(v)
		return reply(map[string]any{"rows": [][]json.RawMessage{{b}}})
	}
	switch {
	case strings.HasPrefix(sql, "DELETE FROM item WHERE source"):
		src := argStr(0)
		f.items = slices.DeleteFunc(f.items, func(it item) bool { return it.Source == src })
	case strings.HasPrefix(sql, "INSERT INTO item"):
		var in []item
		if err := json.Unmarshal([]byte(argStr(0)), &in); err != nil {
			f.t.Fatalf("fake: bad item json: %v", err)
		}
		f.items = append(f.items, in...)
	case strings.Contains(sql, "FROM item WHERE source"):
		src := argStr(0)
		var limit int
		_ = json.Unmarshal(args[1], &limit)
		rows := f.rows(src)
		sort.SliceStable(rows, func(i, j int) bool {
			a, b := rows[i], rows[j]
			if (a.PublishedAt == "") != (b.PublishedAt == "") {
				return a.PublishedAt != ""
			}
			if a.PublishedAt != b.PublishedAt {
				return a.PublishedAt > b.PublishedAt
			}
			return a.Position < b.Position
		})
		if len(rows) > limit {
			rows = rows[:limit]
		}
		if rows == nil {
			rows = []item{}
		}
		return cell(rows)
	case strings.HasPrefix(sql, "INSERT INTO source_status"):
		src := argStr(0)
		st := f.status[src]
		st.Source = src
		if string(args[1]) != "null" {
			st.RefreshedAt = argStr(1)
		}
		st.Error = argStr(2)
		f.status[src] = st
	case strings.Contains(sql, "FROM source_status WHERE source"):
		rows := []sourceStatus{}
		if st, ok := f.status[argStr(0)]; ok {
			rows = append(rows, st)
		}
		return cell(rows)
	default:
		f.t.Fatalf("fake: unexpected %s %q", fn, sql)
	}
	return reply(map[string]any{"rows_affected": 1})
}
