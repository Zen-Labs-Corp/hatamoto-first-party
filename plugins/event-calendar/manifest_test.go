package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

type manifestFile struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	SDK            string   `json:"sdk"`
	Permissions    []string `json:"permissions"`
	AllowedHosts   []string `json:"allowed_hosts"`
	Funcs          []string `json:"funcs"`
	BuilderSources []struct {
		Name   string `json:"name"`
		Params []struct {
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"params"`
	} `json:"builder_sources"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	Routes []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	UI struct {
		Title string   `json:"title"`
		Entry string   `json:"entry"`
		Roles []string `json:"roles"`
	} `json:"ui"`
	Runs []struct {
		Name   string   `json:"name"`
		Stages []string `json:"stages"`
		Inputs []struct {
			Name     string `json:"name"`
			Type     string `json:"type"`
			Required bool   `json:"required"`
		} `json:"inputs"`
	} `json:"runs"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Settings struct {
		Properties map[string]struct {
			Type    string   `json:"type"`
			Default any      `json:"default"`
			Pattern string   `json:"pattern"`
			Enum    []string `json:"enum"`
		} `json:"properties"`
	} `json:"settings_schema"`
}

func loadManifest(t *testing.T) (manifestFile, map[string]json.RawMessage) {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifestFile
	var keys map[string]json.RawMessage
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(raw, &keys); err != nil {
		t.Fatal(err)
	}
	return m, keys
}

func exists(t *testing.T, path string) {
	t.Helper()
	if _, err := os.Stat(path); err != nil {
		t.Errorf("%s: %v", path, err)
	}
}

func TestManifest(t *testing.T) {
	m, _ := loadManifest(t)
	if m.Name != "event-calendar" || m.Version != "0.4.0" {
		t.Errorf("name %q version %q", m.Name, m.Version)
	}
	if icsProdID != "-//Hatamoto//event-calendar "+m.Version+"//EN" {
		t.Errorf("PRODID %q does not follow version %s", icsProdID, m.Version)
	}

	// sdk range (npm caret on 0.x: ^0.M is >=0.M.0 <0.(M+1).0) holds the go.mod version
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	got := regexp.MustCompile(`hatamoto\.dev/sdk v(\d+\.\d+\.\d+)`).FindSubmatch(mod)
	if m.SDK != "^0.21" || got == nil || !strings.HasPrefix(string(got[1]), "0.21.") {
		t.Errorf("sdk range %q, go.mod %q", m.SDK, got)
	}

	wantPerms := []string{"builder.data", "builder.island", "db", "entities.read", "http.fetch", "settings"}
	perms := slices.Sorted(slices.Values(m.Permissions))
	if !slices.Equal(perms, wantPerms) {
		t.Errorf("permissions = %v, want %v", perms, wantPerms)
	}
	if !slices.Equal(m.AllowedHosts, []string{"nominatim.openstreetmap.org"}) {
		t.Errorf("allowed_hosts = %v", m.AllowedHosts)
	}
	if len(m.Cron) != 1 || m.Cron[0].Name != "reconcile" || m.Cron[0].Every != "1h" {
		t.Errorf("cron = %+v", m.Cron)
	}
	if !slices.Equal(m.Funcs, []string{"when", "addlink"}) {
		t.Errorf("funcs = %v", m.Funcs)
	}
	var sources []string
	wantParams := "category:text venue:text city:text free:bool"
	for _, s := range m.BuilderSources {
		sources = append(sources, s.Name)
		var params []string
		for _, p := range s.Params {
			params = append(params, p.Name+":"+p.Type)
		}
		want := wantParams
		if s.Name == "event" {
			want = ""
		}
		if len(s.Params) > 8 || strings.Join(params, " ") != want {
			t.Errorf("source %s params = %v, want %q", s.Name, params, want)
		}
	}
	if !slices.Equal(sources, []string{"upcoming", "past", "event"}) || len(sources) > 8 {
		t.Errorf("builder_sources = %v", sources)
	}

	known := []string{"builder.data", "jsonld.enrich", "entity.created", "entity.updated", "entity.published", "entity.deleted", "import.completed", "on_run_step"}
	for _, f := range m.Funcs {
		known = append(known, "func_"+f)
	}
	for _, c := range m.Cron {
		known = append(known, "on_cron_"+c.Name)
	}
	for k := range m.Limits.HookTimeouts {
		if !slices.Contains(known, k) {
			t.Errorf("hook timeout for unknown %q", k)
		}
	}
	if v := m.Limits.HookTimeouts["builder.data"]; v <= 0 || v > 500 {
		t.Errorf("builder.data = %d ms", v)
	}

	show := m.Settings.Properties["show_map"]
	if show.Type != "boolean" || show.Default != false {
		t.Errorf("show_map = %+v", show)
	}
	if _, ok := m.Settings.Properties["geocode_contact"]; !ok {
		t.Error("geocode_contact missing")
	}
	slug := m.Settings.Properties["schema_slug"]
	if slug.Default != "" || !regexp.MustCompile(slug.Pattern).MatchString("") {
		t.Errorf("schema_slug = %+v", slug)
	}

	week := m.Settings.Properties["week_starts_on"]
	if week.Type != "string" || week.Default != "monday" || !slices.Equal(week.Enum, []string{"monday", "sunday"}) {
		t.Errorf("week_starts_on = %+v", week)
	}

	wantRoutes := []struct {
		method, path string
		public       bool
	}{
		{"GET", "/events.ics", true}, {"GET", "/event/{file}", true}, {"GET", "/api/events", true},
		{"GET", "/series/{slug}", true}, {"GET", "/venue/{slug}", true}, {"GET", "/organizer/{slug}", true},
		{"GET", "/admin/repeating", false},
	}
	if len(m.Routes) != len(wantRoutes) {
		t.Fatalf("routes = %+v", m.Routes)
	}
	for i, r := range m.Routes {
		w := wantRoutes[i]
		if r.Method != w.method || r.Path != w.path || r.Public != w.public {
			t.Errorf("route %d = %+v", i, r)
		}
		if wantRoles := []string{"owner", "editor"}; !w.public && !slices.Equal(r.Roles, wantRoles) || w.public && len(r.Roles) != 0 {
			t.Errorf("route %s roles = %v", r.Path, r.Roles)
		}
	}

	if m.UI.Title != "Repeating events" || m.UI.Entry != "index.html" || !slices.Equal(m.UI.Roles, []string{"owner", "editor"}) {
		t.Errorf("ui = %+v", m.UI)
	}
	exists(t, "ui/"+m.UI.Entry)
	wantRuns := map[string]string{
		"edit_dates": "event:text:true action:select:true date:text:false start_at:text:false end_at:text:false shift_days:number:false",
	}
	if len(m.Runs) != len(wantRuns) {
		t.Fatalf("runs = %+v", m.Runs)
	}
	for _, r := range m.Runs {
		if !slices.Equal(r.Stages, []string{"fetch", "transform"}) {
			t.Errorf("run %s stages = %v", r.Name, r.Stages)
		}
		var in []string
		for _, i := range r.Inputs {
			in = append(in, fmt.Sprintf("%s:%s:%t", i.Name, i.Type, i.Required))
		}
		if got := strings.Join(in, " "); got != wantRuns[r.Name] {
			t.Errorf("run %s inputs = %q, want %q", r.Name, got, wantRuns[r.Name])
		}
	}
	if m.Limits.HookTimeouts["on_run_step"] != 10000 {
		t.Errorf("on_run_step timeout = %d", m.Limits.HookTimeouts["on_run_step"])
	}
}

func TestShippedFiles(t *testing.T) {
	schemas, _ := filepath.Glob("schemas/*.json")
	if len(schemas) != 5 {
		t.Errorf("schemas = %v", schemas)
	}
	exists(t, "migrations/001_init.sql")
	exists(t, "migrations/002_view_columns.sql")
	exists(t, "widgets/event-details/widget.html")
	exists(t, "widgets/event-card/widget.html")
	exists(t, "partials/event_details.html")
}
