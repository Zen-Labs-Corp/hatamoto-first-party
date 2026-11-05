package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// call is one recorded host call.
type call struct {
	Fn   string
	Args map[string]any
}

func (c call) sql() string { s, _ := c.Args["sql"].(string); return s }

func (c call) params() []any { a, _ := c.Args["args"].([]any); return a }

// fakeHost records every host call and answers through reply (nil answers
// nothing). It is installed as callHost for the test, and every test that
// touches the host uses it.
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

// logs returns every logged message.
func (f *fakeHost) logs() []string {
	var out []string
	for _, c := range f.calls {
		if c.Fn == "log" {
			m, _ := c.Args["message"].(string)
			out = append(out, m)
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

// setNow fixes the clock at s (RFC3339) and returns it.
func setNow(t *testing.T, s string) time.Time {
	t.Helper()
	at, err := time.Parse(time.RFC3339, s)
	if err != nil {
		t.Fatal(err)
	}
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { return at }
	return at
}

// oneStatement fails when sql is more than one statement, builds a value into
// the text, or uses a word the host's SQL guard refuses.
func oneStatement(t *testing.T, sql string) {
	t.Helper()
	if strings.Contains(strings.TrimRight(strings.TrimSpace(sql), ";"), ";") {
		t.Fatalf("more than one statement: %s", sql)
	}
	for _, bad := range []string{"public.", "pg_", "current_setting"} {
		if strings.Contains(sql, bad) {
			t.Fatalf("%q in %s", bad, sql)
		}
	}
}

func TestDBJSONUnwrapsValueAndString(t *testing.T) {
	for name, v := range map[string]string{
		"jsonb value": `[{"n":1}]`,
		"json string": `"[{\"n\":1}]"`,
	} {
		newFakeHost(t, func(c call, out any) error { return cell(out, v) })
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
	if c := f.calls[0]; c.Fn != "db.exec" || len(c.params()) != 1 {
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
			b := []byte(`{"found":false}`)
			if ok {
				b, _ = json.Marshal(map[string]any{"found": true, "value": v})
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

func TestHostErrCode(t *testing.T) {
	if got := hostErrCode(hostErr(sdk.ErrTimeout, "call timed out")); got != sdk.ErrTimeout {
		t.Fatalf("got %q", got)
	}
	wrapped := errors.Join(errors.New("x"), hostErr(sdk.ErrBlocked, "no"))
	if got := hostErrCode(wrapped); got != sdk.ErrBlocked {
		t.Fatalf("wrapped: %q", got)
	}
	if got := hostErrCode(errors.New("plain")); got != "" {
		t.Fatalf("plain: %q", got)
	}
	if got := hostErrMessage(hostErr(sdk.ErrBlocked, "private address")); got != "private address" {
		t.Fatalf("message %q", got)
	}
}

func TestFetchAndMailSendCallShape(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		if c.Fn == "http.fetch" {
			return json.Unmarshal([]byte(`{"status":204,"body":"b","truncated":true}`), out)
		}
		return nil
	})
	res, err := fetch(fetchReq{Method: "HEAD", URL: "https://example.org/", TimeoutMS: 5000})
	if err != nil || res.Status != 204 || res.Body != "b" || !res.Truncated {
		t.Fatalf("got %+v %v", res, err)
	}
	if err := mailSend([]string{"a@example.org"}, "subject", "text"); err != nil {
		t.Fatal(err)
	}
	a := f.calls[0].Args
	if a["method"] != "HEAD" || a["url"] != "https://example.org/" || a["timeout_ms"] != float64(5000) {
		t.Fatalf("fetch args %v", a)
	}
	m := f.calls[1].Args
	if m["subject"] != "subject" || m["text"] != "text" || len(m["to"].([]any)) != 1 {
		t.Fatalf("mail args %v", m)
	}
}
