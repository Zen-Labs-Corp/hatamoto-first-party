package main

import (
	"encoding/json"
	"errors"
	"testing"
)

func TestDBJSONUnwrapsValueAndString(t *testing.T) {
	for name, cell := range map[string]string{
		"jsonb value": `[{"n":1}]`,
		"json string": `"[{\"n\":1}]"`,
	} {
		callHost = func(fn string, _, out any) error {
			if fn != "db.query" {
				t.Fatalf("host call %q", fn)
			}
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		}
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
	callHost = func(_ string, _, out any) error {
		return json.Unmarshal([]byte(`{"rows":[]}`), out)
	}
	if err := dbJSON("SELECT 1", new(any)); err == nil {
		t.Fatal("want an error for no rows")
	}
}

func TestDBExecPassesStatementAndArgs(t *testing.T) {
	var fn string
	var args map[string]any
	callHost = func(f string, a, _ any) error {
		fn, args = f, a.(map[string]any)
		return errors.New("boom")
	}
	if err := dbExec("DELETE FROM t WHERE a = $1", 1); err == nil {
		t.Fatal("host error must surface")
	}
	if fn != "db.exec" || args["sql"] != "DELETE FROM t WHERE a = $1" {
		t.Fatalf("got %q %v", fn, args)
	}
	if a, _ := args["args"].([]any); len(a) != 1 || a[0] != 1 {
		t.Fatalf("args = %v", args["args"])
	}
}

func TestDBExecRowsDecodesRowsAffected(t *testing.T) {
	for _, n := range []int64{0, 1} {
		callHost = func(fn string, _, out any) error {
			if fn != "db.exec" {
				t.Fatalf("host call %q", fn)
			}
			b, _ := json.Marshal(map[string]int64{"rows_affected": n})
			return json.Unmarshal(b, out)
		}
		got, err := dbExecRows("INSERT INTO t VALUES ($1) ON CONFLICT DO NOTHING", "x")
		if err != nil || got != n {
			t.Fatalf("dbExecRows = %d, %v; want %d", got, err, n)
		}
	}
}

func TestPostJSONSendsPostWithTimeout(t *testing.T) {
	var fn string
	var args map[string]any
	callHost = func(f string, a, out any) error {
		fn, args = f, a.(map[string]any)
		return json.Unmarshal([]byte(`{"status":400,"body":"{}","truncated":false}`), out)
	}
	res, err := postJSON("https://graph.example.com/x", []byte(`{"a":1}`))
	if err != nil || res.Status != 400 || res.Body != "{}" {
		t.Fatalf("postJSON = %+v, %v", res, err)
	}
	if fn != "http.fetch" || args["method"] != "POST" || args["url"] != "https://graph.example.com/x" ||
		args["timeout_ms"] != 8000 || args["body"] != `{"a":1}` {
		t.Fatalf("got %q %v", fn, args)
	}
	if h, _ := args["headers"].(map[string]string); h["Content-Type"] != "application/json" {
		t.Fatalf("headers = %v", args["headers"])
	}
}

func TestPostJSONHostError(t *testing.T) {
	callHost = func(string, any, any) error { return errors.New("blocked") }
	if _, err := postJSON("https://graph.example.com/x", nil); err == nil {
		t.Fatal("host error must surface")
	}
}

func TestLogfIgnoresHostError(t *testing.T) {
	var args map[string]string
	callHost = func(fn string, a, _ any) error {
		args = a.(map[string]string)
		return errors.New("rate_limited")
	}
	logf("error", "delivery %s", "failed")
	if args["level"] != "error" || args["message"] != "delivery failed" {
		t.Fatalf("args = %v", args)
	}
}
