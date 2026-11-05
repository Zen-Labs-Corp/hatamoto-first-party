package main

import (
	"encoding/json"
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
