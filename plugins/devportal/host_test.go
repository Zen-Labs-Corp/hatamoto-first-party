package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
)

// hostCall is one recorded host call.
type hostCall struct {
	fn   string
	sql  string
	args []any
}

// fakeHost replaces callHost. settings is the settings.get answer (a JSON
// object) unless settingsErr is set; db answers every db.* call.
type fakeHost struct {
	settings    string
	settingsErr error
	db          func(c hostCall, out any) error
	calls       []hostCall
}

func (f *fakeHost) install(t *testing.T) {
	t.Helper()
	old := callHost
	t.Cleanup(func() { callHost = old })
	callHost = func(fn string, args, out any) error {
		c := hostCall{fn: fn}
		if m, ok := args.(map[string]any); ok {
			c.sql, _ = m["sql"].(string)
			c.args, _ = m["args"].([]any)
		}
		f.calls = append(f.calls, c)
		if fn == "settings.get" {
			if f.settingsErr != nil {
				return f.settingsErr
			}
			return json.Unmarshal([]byte(f.settings), out)
		}
		if f.db != nil {
			return f.db(c, out)
		}
		if fn == "db.exec" && out != nil {
			return json.Unmarshal([]byte(`{"rows_affected":1}`), out)
		}
		return nil
	}
}

// sqlCalls returns the recorded db statements.
func (f *fakeHost) sqlCalls() []hostCall {
	var out []hostCall
	for _, c := range f.calls {
		if strings.HasPrefix(c.fn, "db.") {
			out = append(out, c)
		}
	}
	return out
}

// cell answers a db.query with one jsonb cell.
func cell(out any, doc string) error {
	return json.Unmarshal([]byte(`{"rows":[[`+doc+`]]}`), out)
}

// affected answers a db.exec.
func affected(out any, n int) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(`{"rows_affected":`+string(rune('0'+n))+`}`), out)
}

var (
	guardFirstWord = regexp.MustCompile(`(?i)^(select|insert|update|delete|with|values)\b`)
	guardBanned    = regexp.MustCompile(`(?i)\bpublic\b|pg_|information_schema|set_config|current_setting|session_user|\bcopy\b|dblink`)
)

// sqlGuardOK reports whether sql looks like one statement the plugin role may
// run.
func sqlGuardOK(sql string) bool {
	s := strings.TrimSpace(sql)
	return guardFirstWord.MatchString(s) &&
		!strings.Contains(strings.TrimSuffix(s, ";"), ";") &&
		!guardBanned.MatchString(s) &&
		!strings.Contains(s, "SELECT *")
}

func TestSQLGuard(t *testing.T) {
	for sql, want := range map[string]bool{
		"SELECT 1":                true,
		"DROP TABLE x":            false,
		"SELECT 1; DELETE FROM x": false,
		"SELECT * FROM x":         false,
		"SELECT a FROM public.x":  false,
		"SELECT a FROM pg_user":   false,
		"UPDATE x SET a = 1":      true,
	} {
		if got := sqlGuardOK(sql); got != want {
			t.Errorf("sqlGuardOK(%q) = %v", sql, got)
		}
	}
}

func TestDBJSONUnwrapsValueAndString(t *testing.T) {
	for name, doc := range map[string]string{
		"jsonb value": `[{"n":1}]`,
		"json string": `"[{\"n\":1}]"`,
	} {
		(&fakeHost{db: func(_ hostCall, out any) error { return cell(out, doc) }}).install(t)
		var got []struct{ N int }
		if err := dbJSON("SELECT 1", &got); err != nil || len(got) != 1 || got[0].N != 1 {
			t.Fatalf("%s: %+v, %v", name, got, err)
		}
	}
}

func TestDBJSONNoRowsAndHostError(t *testing.T) {
	(&fakeHost{db: func(_ hostCall, out any) error { return json.Unmarshal([]byte(`{"rows":[]}`), out) }}).install(t)
	if err := dbJSON("SELECT 1", new(any)); err == nil {
		t.Fatal("want an error for no rows")
	}
	(&fakeHost{db: func(hostCall, any) error { return errors.New("boom") }}).install(t)
	if err := dbExec("DELETE FROM t WHERE a = $1", 1); err == nil {
		t.Fatal("host error must surface")
	}
}
