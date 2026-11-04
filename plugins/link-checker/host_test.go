package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

// call is one recorded host call.
type call struct {
	Fn   string
	Args map[string]any
}

func (c call) sql() string { s, _ := c.Args["sql"].(string); return s }

func (c call) params() []any { a, _ := c.Args["args"].([]any); return a }

// fakeHost records every host call and answers through reply (nil answers
// nothing). It is installed as callHost for the test.
type fakeHost struct {
	calls []call
	reply func(c call, out any) error
}

func newFakeHost(t *testing.T, reply func(c call, out any) error) *fakeHost {
	t.Helper()
	f := &fakeHost{reply: reply}
	oldHost, oldNow := callHost, now
	t.Cleanup(func() { callHost, now = oldHost, oldNow })
	callHost = func(fn string, args, out any) error {
		raw, _ := json.Marshal(args)
		var m map[string]any
		_ = json.Unmarshal(raw, &m)
		c := call{Fn: fn, Args: m}
		f.calls = append(f.calls, c)
		if fn == "log" || f.reply == nil {
			return nil
		}
		return f.reply(c, out)
	}
	return f
}

func (f *fakeHost) count(fn string) int {
	n := 0
	for _, c := range f.calls {
		if c.Fn == fn {
			n++
		}
	}
	return n
}

// sqls returns the SQL of every db call, in order.
func (f *fakeHost) sqls() []string {
	var out []string
	for _, c := range f.calls {
		if c.Fn == "db.exec" || c.Fn == "db.query" {
			out = append(out, c.sql())
		}
	}
	return out
}

// cell answers a db.query with one jsonb cell.
func cell(out any, v string) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(`{"rows":[[`+v+`]]}`), out)
}

func fixedNow(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	now = func() time.Time { return at }
	return at
}

func TestDBJSONUnwrapsValueAndString(t *testing.T) {
	for name, v := range map[string]string{
		"jsonb value": `[{"n":1}]`,
		"json string": `"[{\"n\":1}]"`,
	} {
		newFakeHost(t, func(c call, out any) error {
			if c.Fn != "db.query" {
				t.Fatalf("host call %q", c.Fn)
			}
			return cell(out, v)
		})
		var got []struct{ N int }
		if err := dbJSON("SELECT 1", &got); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if len(got) != 1 || got[0].N != 1 {
			t.Fatalf("%s: got %+v", name, got)
		}
	}
}

func TestDBJSONNoRows(t *testing.T) {
	newFakeHost(t, func(_ call, out any) error { return json.Unmarshal([]byte(`{"rows":[]}`), out) })
	if err := dbJSON("SELECT 1", new(any)); err == nil {
		t.Fatal("want an error for no rows")
	}
}

func TestDBExecSurfacesHostError(t *testing.T) {
	f := newFakeHost(t, func(call, any) error { return errors.New("boom") })
	if err := dbExec("DELETE FROM t WHERE a = $1", 1); err == nil {
		t.Fatal("host error must surface")
	}
	if c := f.calls[0]; c.Fn != "db.exec" || c.sql() != "DELETE FROM t WHERE a = $1" || len(c.params()) != 1 {
		t.Fatalf("got %+v", c)
	}
}

func TestKV(t *testing.T) {
	stored := map[string]json.RawMessage{}
	newFakeHost(t, func(c call, out any) error {
		key, _ := c.Args["key"].(string)
		switch c.Fn {
		case "kv.set":
			stored[key], _ = json.Marshal(c.Args["value"])
		case "kv.delete":
			delete(stored, key)
		case "kv.get":
			v, ok := stored[key]
			b, _ := json.Marshal(map[string]any{"found": ok, "value": v})
			if !ok {
				b = []byte(`{"found":false}`)
			}
			return json.Unmarshal(b, out)
		}
		return nil
	})
	var v struct{ A int }
	if ok, err := kvGet("k", &v); ok || err != nil {
		t.Fatalf("missing key: %v %v", ok, err)
	}
	if err := kvSet("k", map[string]int{"A": 3}); err != nil {
		t.Fatal(err)
	}
	if ok, err := kvGet("k", &v); !ok || err != nil || v.A != 3 {
		t.Fatalf("got %v %v %+v", ok, err, v)
	}
	_ = kvDelete("k")
	if ok, _ := kvGet("k", &v); ok {
		t.Fatal("deleted key still found")
	}
}

func TestLoadSettingsClampsAndFailsOpen(t *testing.T) {
	newFakeHost(t, func(_ call, out any) error {
		return json.Unmarshal([]byte(`{"exclude":["/a"," ",""," https://example.org/x "],"recheck_days":500,"timeout_ms":5}`), out)
	})
	s := loadSettings()
	if s.RecheckDays != 7 || s.TimeoutMS != 5000 || len(s.Exclude) != 2 || s.Exclude[1] != "https://example.org/x" {
		t.Fatalf("got %+v", s)
	}
	newFakeHost(t, func(call, any) error { return errors.New("down") })
	if s := loadSettings(); s.RecheckDays != 7 || s.TimeoutMS != 5000 {
		t.Fatalf("defaults expected, got %+v", s)
	}
	newFakeHost(t, func(_ call, out any) error {
		return json.Unmarshal([]byte(`{"recheck_days":30,"timeout_ms":2000}`), out)
	})
	if s := loadSettings(); s.RecheckDays != 30 || s.TimeoutMS != 2000 {
		t.Fatalf("got %+v", s)
	}
}

func oneStatement(t *testing.T, sql string) {
	t.Helper()
	if strings.Contains(strings.TrimSpace(strings.TrimRight(strings.TrimSpace(sql), ";")), ";") {
		t.Fatalf("more than one statement: %s", sql)
	}
	for _, bad := range []string{"public.", "pg_", "current_setting"} {
		if strings.Contains(sql, bad) {
			t.Fatalf("%q in %s", bad, sql)
		}
	}
}
