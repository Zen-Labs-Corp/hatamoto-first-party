package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// splitsHost fakes the host for the split rule routes.
type splitsHost struct {
	list  string
	calls []call
}

func newSplitsHost(t *testing.T) *splitsHost {
	t.Helper()
	h := &splitsHost{list: `[]`}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			return jsonInto(gigEntity, out)
		case strings.HasPrefix(sql, "WITH d AS (DELETE FROM split_rules"):
			return nil
		case strings.Contains(sql, "FROM split_rules"):
			return jsonInto(`{"rows":[[`+h.list+`]]}`, out)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *splitsHost) stmt(prefix string) *call {
	for i, c := range h.calls {
		if strings.HasPrefix(c.sql, prefix) {
			return &h.calls[i]
		}
	}
	return nil
}

func TestSaveSplits(t *testing.T) {
	h := newSplitsHost(t)
	h.list = `[{"id":1,"name":"Jazz Society","email":"pay@example.org","kind":"percent","value":8000}]`
	code, body := post(t, "/admin/events/{id}/splits", "e1", owner,
		`{"rules":[{"name":" Jazz Society ","email":"pay@example.org","kind":"percent","value":8000},{"name":"Co-host","email":"co@example.org","kind":"fixed","value":250}]}`)
	if code != 200 || len(body["splits"].([]any)) != 1 {
		t.Fatalf("%d %v", code, body)
	}
	st := h.stmt("WITH d AS (DELETE FROM split_rules WHERE event_id = $1)")
	if st == nil || st.args[0] != "e1" || !strings.Contains(st.args[1].(string), `"name":"Jazz Society"`) || !strings.Contains(st.args[1].(string), `"pos":1`) {
		t.Fatalf("statement %+v", st)
	}
	if code, _ := post(t, "/admin/events/{id}/splits", "e1", owner, `{"rules":[]}`); code != 200 {
		t.Errorf("clear: %d", code)
	}
}

func TestSaveSplitsRefusesBadRules(t *testing.T) {
	h := newSplitsHost(t)
	rule := func(name, email, kind, value string) string {
		return `{"name":"` + name + `","email":"` + email + `","kind":"` + kind + `","value":` + value + `}`
	}
	many := strings.Repeat(rule("A", "a@example.org", "fixed", "1")+",", 10) + rule("A", "b@example.org", "fixed", "1")
	for name, body := range map[string]string{
		"11 rules":            `{"rules":[` + many + `]}`,
		"bad email":           `{"rules":[` + rule("A", "not an email", "fixed", "1") + `]}`,
		"display name":        `{"rules":[` + rule("A", "A <a@example.org>", "fixed", "1") + `]}`,
		"long email":          `{"rules":[` + rule("A", strings.Repeat("a", 250)+"@example.org", "fixed", "1") + `]}`,
		"dup email":           `{"rules":[` + rule("A", "a@example.org", "fixed", "1") + `,` + rule("B", "A@Example.org", "fixed", "1") + `]}`,
		"percent 10001":       `{"rules":[` + rule("A", "a@example.org", "percent", "5000") + `,` + rule("B", "b@example.org", "percent", "5001") + `]}`,
		"percent 10001 alone": `{"rules":[` + rule("A", "a@example.org", "percent", "10001") + `]}`,
		"kind":                `{"rules":[` + rule("A", "a@example.org", "x", "1") + `]}`,
		"value 0":             `{"rules":[` + rule("A", "a@example.org", "fixed", "0") + `]}`,
		"fixed too big":       `{"rules":[` + rule("A", "a@example.org", "fixed", "100000000") + `]}`,
		"empty name":          `{"rules":[` + rule(" ", "a@example.org", "fixed", "1") + `]}`,
		"unknown key":         `{"rules":[],"x":1}`,
	} {
		if code, _ := post(t, "/admin/events/{id}/splits", "e1", owner, body); code != 400 {
			t.Errorf("%s: %d", name, code)
		}
	}
	if code, _ := post(t, "/admin/events/{id}/splits", "a b", owner, `{"rules":[]}`); code != 400 {
		t.Errorf("bad event id: %d", code)
	}
	if h.stmt("WITH d AS (DELETE FROM split_rules") != nil {
		t.Error("a bad body wrote")
	}
	// exactly 100 % in total is fine
	ok := `{"rules":[` + rule("A", "a@example.org", "percent", "6000") + `,` + rule("B", "b@example.org", "percent", "4000") + `]}`
	if code, _ := post(t, "/admin/events/{id}/splits", "e1", owner, ok); code != 200 {
		t.Errorf("100 %%: %d", code)
	}
}

func TestSaveSplitsUnknownEvent(t *testing.T) {
	newSplitsHost(t)
	callInner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == sdk.FnEntitiesGet {
			return &sdk.HostError{Code: sdk.ErrNotFound}
		}
		return callInner(fn, args, out)
	}
	if code, _ := post(t, "/admin/events/{id}/splits", "e1", owner, `{"rules":[]}`); code != 404 {
		t.Errorf("unknown event: %d", code)
	}
}

func TestSplitsAreOwnerOnly(t *testing.T) {
	h := newSplitsHost(t)
	for _, u := range []*sdk.HTTPUser{editor, nil} {
		if code, _ := post(t, "/admin/events/{id}/splits", "e1", u, `{"rules":[]}`); code != 403 {
			t.Errorf("as %v: %d", u, code)
		}
	}
	for _, c := range h.calls {
		if !strings.HasPrefix(c.sql, "SELECT") {
			t.Errorf("a forbidden request wrote: %q", c.sql)
		}
	}
}

// Payout addresses are personal data: only the owner's event detail carries them.
func TestEventDetailSplitsOnlyForOwner(t *testing.T) {
	newAdminHost(t)
	_, body := get(t, "/admin/events/{id}", "e1", editor, nil)
	if _, has := body["splits"]; has || body["event"] == nil {
		t.Errorf("editor detail: %v", body)
	}
	_, body = get(t, "/admin/events/{id}", "e1", owner, nil)
	if s, has := body["splits"]; !has || len(s.([]any)) != 0 {
		t.Errorf("owner detail: %v", body)
	}
}
