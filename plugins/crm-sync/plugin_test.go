package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

type hostCallRec struct {
	fn   string
	sql  string
	args []any
	msg  string
}

// fakeHost answers settings.get, forms.list, db, kv and log. Every call to
// db.exec, db.query and log is recorded.
type fakeHost struct {
	settings  string
	forms     string
	dup       bool
	queryRows []string
	kv        map[string]json.RawMessage
	kvTTL     map[string]int
	calls     []hostCallRec
}

func (h *fakeHost) install(t *testing.T) {
	t.Helper()
	h.kv = map[string]json.RawMessage{}
	h.kvTTL = map[string]int{}
	callHost = func(fn string, a, out any) error {
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(h.settings), out)
		case "forms.list":
			if h.forms == "" {
				return json.Unmarshal([]byte(`{"items":[]}`), out)
			}
			return json.Unmarshal([]byte(h.forms), out)
		case "db.exec":
			m := a.(map[string]any)
			h.calls = append(h.calls, hostCallRec{fn: fn, sql: m["sql"].(string), args: m["args"].([]any)})
			if out != nil {
				n := "1"
				if h.dup {
					n = "0"
				}
				return json.Unmarshal([]byte(`{"rows_affected":`+n+`}`), out)
			}
		case "db.query":
			m := a.(map[string]any)
			h.calls = append(h.calls, hostCallRec{fn: fn, sql: m["sql"].(string), args: m["args"].([]any)})
			if len(h.queryRows) == 0 {
				t.Fatalf("unscripted db.query %q", m["sql"])
			}
			row := h.queryRows[0]
			h.queryRows = h.queryRows[1:]
			return json.Unmarshal([]byte(`{"rows":[[`+row+`]]}`), out)
		case "kv.get":
			key := a.(map[string]string)["key"]
			v, ok := h.kv[key]
			if !ok {
				return json.Unmarshal([]byte(`{"found":false}`), out)
			}
			b, _ := json.Marshal(map[string]any{"found": true, "value": v})
			return json.Unmarshal(b, out)
		case "kv.set":
			m := a.(map[string]any)
			b, _ := json.Marshal(m["value"])
			key := m["key"].(string)
			h.kv[key] = b
			h.kvTTL[key], _ = m["ttl_seconds"].(int)
		case "kv.delete":
			key := a.(map[string]string)["key"]
			delete(h.kv, key)
			delete(h.kvTTL, key)
		case "log":
			h.calls = append(h.calls, hostCallRec{fn: fn, msg: a.(map[string]string)["message"]})
		default:
			t.Fatalf("unexpected host call %q", fn)
		}
		return nil
	}
	now = func() time.Time { return time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC) }
	budgetEnd = time.Time{}
	t.Cleanup(func() { now = time.Now; budgetEnd = time.Time{} })
}

// fakeFetch answers requests from a script, one response per call (or from
// handler when set), and records them.
type fakeFetch struct {
	t       *testing.T
	resp    []fetchResp
	handler func(fetchReq) fetchResp
	reqs    []fetchReq
}

func (f *fakeFetch) fetch(r fetchReq) fetchResp {
	f.reqs = append(f.reqs, r)
	if f.handler != nil {
		return f.handler(r)
	}
	if len(f.reqs) > len(f.resp) {
		f.t.Fatalf("unexpected extra request %s %s", r.Method, r.URL)
	}
	return f.resp[len(f.reqs)-1]
}

func newFake(t *testing.T, resp ...fetchResp) *fakeFetch { return &fakeFetch{t: t, resp: resp} }

func jsonOf(t *testing.T, s string) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(s), &m); err != nil {
		t.Fatalf("body %q: %v", s, err)
	}
	return m
}

// failureCases are the outcomes every provider must give for a failing first call.
var failureCases = []struct {
	name string
	resp fetchResp
	kind string
}{
	{"400", fetchResp{Status: 400}, kindFail},
	{"401", fetchResp{Status: 401}, kindFail},
	{"404", fetchResp{Status: 404}, kindFail},
	{"422", fetchResp{Status: 422}, kindFail},
	{"408", fetchResp{Status: 408}, kindRetry},
	{"429", fetchResp{Status: 429}, kindRetry},
	{"500", fetchResp{Status: 500}, kindRetry},
	{"503", fetchResp{Status: 503}, kindRetry},
	{"timeout", fetchResp{Code: sdk.ErrTimeout}, kindRetry},
	{"unavailable", fetchResp{Code: sdk.ErrUnavailable}, kindRetry},
	{"rate_limited", fetchResp{Code: sdk.ErrRateLimited}, kindRetry},
	{"capability_denied", fetchResp{Code: sdk.ErrCapabilityDenied}, kindFail},
	{"blocked", fetchResp{Code: sdk.ErrBlocked}, kindFail},
	{"invalid_argument", fetchResp{Code: sdk.ErrInvalidArgument}, kindFail},
	{"budget", fetchResp{Code: budgetCode}, kindBudget},
}

// logRows returns the (status, message) of every log insert, assuming the
// statement's args start with created_at, submission_id, form_slug, provider,
// status, record_id, record_url, code, message.
func (h *fakeHost) logRows() [][2]string {
	var rows [][2]string
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql, "INSERT INTO log") {
			rows = append(rows, [2]string{c.args[4].(string), c.args[8].(string)})
		}
	}
	return rows
}

func (h *fakeHost) hostLogs() []string {
	var out []string
	for _, c := range h.calls {
		if c.fn == "log" {
			out = append(out, c.msg)
		}
	}
	return out
}

func (h *fakeHost) fetchCalls() int {
	n := 0
	for _, c := range h.calls {
		if c.fn == "http.fetch" {
			n++
		}
	}
	return n
}
