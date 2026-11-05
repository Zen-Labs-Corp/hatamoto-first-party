package main

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

type hostCallRec struct {
	fn   string
	sql  string
	args []any
	msg  string
	req  map[string]any
}

// fakeHost answers the host functions the plugin uses and records every call
// except settings.get. A test sets the answers it needs before install.
type fakeHost struct {
	settings string
	forms    string // forms.list answer; empty makes the call fail
	subs     string // forms.submissions answer; empty makes the call fail
	query    string // db.query answer, one jsonb cell holding this JSON
	dup      bool   // db.exec reports no inserted row
	calls    []hostCallRec
}

func (h *fakeHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, a, out any) error {
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(h.settings), out)
		case "forms.list":
			h.calls = append(h.calls, hostCallRec{fn: fn})
			return answer(h.forms, out)
		case "forms.submissions":
			h.calls = append(h.calls, hostCallRec{fn: fn, req: a.(map[string]any)})
			return answer(h.subs, out)
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
			return h.dbQuery(m["sql"].(string), m["args"].([]any), out)
		case "log":
			h.calls = append(h.calls, hostCallRec{fn: fn, msg: a.(map[string]string)["message"]})
		default:
			t.Fatalf("unexpected host call %q", fn)
		}
		return nil
	}
	now = func() time.Time { return time.Date(2026, 11, 4, 10, 0, 0, 0, time.UTC) }
	budgetEnd = time.Time{}
	t.Cleanup(func() { now = time.Now; fetchFn = doFetch; budgetEnd = time.Time{} })
}

func answer(s string, out any) error {
	if s == "" {
		return errHost
	}
	return json.Unmarshal([]byte(s), out)
}

var errHost = &hostErr{}

type hostErr struct{}

func (*hostErr) Error() string { return "host down" }

func TestSettingsDefaultsAndChannels(t *testing.T) {
	h := &fakeHost{settings: `{"slack_enabled":true,"slack_webhook_url":"https://hooks.slack.com/x",` +
		`"teams_enabled":true,"gchat_enabled":false,"gchat_webhook_url":"https://chat.googleapis.com/v1/spaces/a/messages?key=k",` +
		`"telegram_enabled":true,"telegram_bot_token":"123456:abcdefghijklmnopqrstuv"}`}
	h.install(t)
	c, err := loadCfg()
	if err != nil {
		t.Fatal(err)
	}
	if got := c.channels(); len(got) != 1 || got[0] != chanSlack {
		t.Fatalf("channels = %v (teams has no URL, gchat is off, telegram has no chat)", got)
	}
	if !c.includeValues() || c.maxLen() != 500 || !c.watched("any") {
		t.Fatal("defaults: include values, 500, all forms")
	}
	c.Forms = []string{" quote "}
	if c.watched("contact") || !c.watched("quote") {
		t.Fatal("watched filter")
	}
}

func unmarshal(s string, out any) error { return json.Unmarshal([]byte(s), out) }

func itoa(n int) string { return strconv.Itoa(n) }

func quote(s string) string { b, _ := json.Marshal(s); return string(b) }

// maxDBResultBytes is the host's cap on one db.query result.
const maxDBResultBytes = 1 << 20

// dbQuery answers a db.query like the host: one jsonb cell, and a result over
// the cap comes back empty with truncated set. h.query holds queue rows; the
// claim statement returns their ids, a single-row read returns that row.
func (h *fakeHost) dbQuery(sql string, args []any, out any) error {
	doc := h.query
	var rows []map[string]json.RawMessage
	if strings.Contains(sql, "FROM queue") && json.Unmarshal([]byte(h.query), &rows) == nil {
		switch {
		case strings.Contains(sql, "jsonb_agg(u.id"):
			ids := []json.RawMessage{}
			for _, r := range rows {
				ids = append(ids, r["id"])
			}
			b, _ := json.Marshal(ids)
			doc = string(b)
		case strings.Contains(sql, "WHERE id = $1"):
			doc = "null"
			for _, r := range rows {
				if string(r["id"]) == fmt.Sprint(args[0]) {
					b, _ := json.Marshal(r)
					doc = string(b)
				}
			}
		}
	}
	cell, _ := json.Marshal(doc)
	if len(cell) > maxDBResultBytes {
		return json.Unmarshal([]byte(`{"rows":[],"truncated":true}`), out)
	}
	return json.Unmarshal([]byte(`{"rows":[[`+string(cell)+`]]}`), out)
}
