package main

import (
	"encoding/json"
	"errors"
	"os"
	"slices"
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
}

type manifest struct {
	Name        string   `json:"name"`
	Permissions []string `json:"permissions"`
	Routes      []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	UI   map[string]any `json:"ui"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestPermissionsAndRoutes(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "stats" {
		t.Fatalf("name %q", m.Name)
	}
	if !slices.Equal(m.Permissions, []string{"db"}) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	var ping, overview bool
	for _, r := range m.Routes {
		switch r.Method + " " + r.Path {
		case "POST /v1/ping":
			ping = r.Public
		case "GET /overview":
			overview = !r.Public && len(r.Roles) == 0
		}
	}
	if !ping || !overview {
		t.Fatalf("ping public=%v, overview owner-only=%v", ping, overview)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

// A restart resets a cron clock, so the rollup runs often enough that daily
// rows cannot outlive their retention for long.
func TestRollupCronIsHourly(t *testing.T) {
	m := loadManifest(t)
	if len(m.Cron) != 1 || m.Cron[0].Name != "rollup" || m.Cron[0].Every != "1h" {
		t.Fatalf("cron %+v", m.Cron)
	}
}
