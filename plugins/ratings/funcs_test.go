package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func rawArgs(vs ...any) []json.RawMessage {
	out := []json.RawMessage{}
	for _, v := range vs {
		b, _ := json.Marshal(v)
		out = append(out, b)
	}
	return out
}

func rowsDB(rows string) func(hostCall, any) error {
	return func(_ hostCall, out any) error {
		return json.Unmarshal([]byte(`{"rows":`+rows+`}`), out)
	}
}

func TestFuncSummary(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []json.RawMessage
		db   func(hostCall, any) error
		want string
		sql  bool
	}{
		{"no votes", rawArgs(testID), rowsDB(`[[0,0]]`), "", true},
		{"no rows", rawArgs(testID), rowsDB(`[]`), "", true},
		{"one vote", rawArgs(testID), rowsDB(`[[1,5]]`), "5.0 out of 5 (1 rating)", true},
		{"many votes", rawArgs(testID), rowsDB(`[[27,"4.2963"]]`), "4.3 out of 5 (27 ratings)", true},
		{"numbers as strings", rawArgs(testID), rowsDB(`[["27","4.3"]]`), "4.3 out of 5 (27 ratings)", true},
		{"short row", rawArgs(testID), rowsDB(`[[3]]`), "", true},
		{"db error", rawArgs(testID), func(hostCall, any) error { return errors.New("unavailable") }, "", true},
		{"bad id", rawArgs("nope"), rowsDB(`[[3,4]]`), "", false},
		{"not a string", rawArgs(7), rowsDB(`[[3,4]]`), "", false},
		{"no args", nil, rowsDB(`[[3,4]]`), "", false},
		{"two args", rawArgs(testID, "x"), rowsDB(`[[3,4]]`), "", false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := &fakeHost{db: tc.db}
			f.install(t)
			got, err := funcSummary(tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
			if queried := len(f.calls) > 0; queried != tc.sql {
				t.Fatalf("queried = %v", queried)
			}
			for _, c := range f.calls {
				if strings.Contains(c.sql, "voter") {
					t.Fatalf("summary must not depend on a voter: %s", c.sql)
				}
			}
		})
	}
}

func TestFuncEnabled(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		err      error
		args     []json.RawMessage
		want     string
	}{
		{"default lists post", `{}`, nil, rawArgs("post"), "1"},
		{"default hides page", `{}`, nil, rawArgs("page"), ""},
		{"configured", `{"schemas":["page"]}`, nil, rawArgs("page"), "1"},
		{"configured other", `{"schemas":["page"]}`, nil, rawArgs("post"), ""},
		{"empty slug", `{}`, nil, rawArgs(""), ""},
		{"settings error uses default", "", errors.New("denied"), rawArgs("post"), "1"},
		{"not a string", `{}`, nil, rawArgs(1), ""},
		{"no args", `{}`, nil, nil, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			(&fakeHost{settings: tc.settings, settingsErr: tc.err}).install(t)
			got, err := funcEnabled(tc.args)
			if err != nil || got != tc.want {
				t.Fatalf("got %q, %v; want %q", got, err, tc.want)
			}
		})
	}
}
