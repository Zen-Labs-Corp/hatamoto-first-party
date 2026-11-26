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
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

type manifestFile struct {
	Name           string   `json:"name"`
	Version        string   `json:"version"`
	Description    string   `json:"description"`
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
		Name             string `json:"name"`
		Cron             string `json:"cron"`
		StandingApproval *struct {
			Fields  []string `json:"fields"`
			Schemas []string `json:"schemas"`
			Create  struct {
				Schemas    []string `json:"schemas"`
				MaxPerHour int      `json:"max_per_hour"`
				LinkFields []string `json:"link_fields"`
			} `json:"create"`
		} `json:"standing_approval"`
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
			Type      string   `json:"type"`
			Default   any      `json:"default"`
			Pattern   string   `json:"pattern"`
			Enum      []string `json:"enum"`
			MaxLen    int      `json:"maxLength"`
			WriteOnly bool     `json:"writeOnly"`
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
	if m.Name != "event-calendar" || m.Version != "0.6.1" {
		t.Errorf("name %q version %q", m.Name, m.Version)
	}
	// the description is shown in the plugin list: short, and without characters the host's text checks refuse
	if n := len(m.Description); n > 200 || strings.ContainsAny(m.Description, "\"\\|&") {
		t.Errorf("description %q: %d bytes, or a quote, backslash, pipe or ampersand", m.Description, n)
	}
	// the registry validator (and core) accept 20..160 characters and no control characters
	if n := utf8.RuneCountInString(m.Description); n < 20 || n > 160 {
		t.Errorf("description is %d characters, want 20..160", n)
	}
	if strings.IndexFunc(m.Description, unicode.IsControl) >= 0 {
		t.Errorf("description %q has a control character", m.Description)
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
	if m.SDK != "^0.28.1" || got == nil || string(got[1]) != "0.28.1" {
		t.Errorf("sdk range %q, go.mod %q", m.SDK, got)
	}

	wantPerms := []string{"builder.data", "builder.island", "db", "entities.read", "http.fetch", "http.fetch.any", "mail.send", "settings"}
	perms := slices.Sorted(slices.Values(m.Permissions))
	if !slices.Equal(perms, wantPerms) {
		t.Errorf("permissions = %v, want %v", perms, wantPerms)
	}
	wantHosts := []string{"nominatim.openstreetmap.org", "calendar.google.com", "outlook.office365.com", "outlook.office.com", "outlook.live.com", "www.meetup.com", "www.eventbrite.com", "www.eventbriteapi.com", "*"}
	if !slices.Equal(m.AllowedHosts, wantHosts) || len(m.AllowedHosts)-1 > 32 {
		t.Errorf("allowed_hosts = %v", m.AllowedHosts)
	}
	if len(m.Cron) != 3 || m.Cron[1].Name != "prune" || m.Cron[1].Every != "1h" || m.Cron[0].Name != "reconcile" || m.Cron[0].Every != "1h" || m.Cron[2].Name != "import" || m.Cron[2].Every != "12h" {
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

	if sub := m.Settings.Properties["submissions_enabled"]; sub.Type != "boolean" || sub.Default != false {
		t.Errorf("submissions_enabled = %+v", sub)
	}

	// core's settings form shows a text field with a maxLength above 200 as a textarea
	if n := m.Settings.Properties["notify_email"]; n.Type != "string" || n.MaxLen <= 0 || n.MaxLen > 200 {
		t.Errorf("notify_email = %+v, want a maxLength of at most 200 so it stays a one-line field", n)
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
		{"GET", "/submit/token", true}, {"POST", "/submit", true},
		{"GET", "/admin/repeating", false}, {"GET", "/admin/submissions", false},
		{"POST", "/admin/submissions/{id}/reject", false}, {"POST", "/admin/submissions/reject-all", false},
		{"POST", "/admin/submissions/{id}/delete", false},
		{"GET", "/admin/imports", false},
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

	if m.UI.Title != "Event calendar" || m.UI.Entry != "index.html" || !slices.Equal(m.UI.Roles, []string{"owner", "editor"}) {
		t.Errorf("ui = %+v", m.UI)
	}
	exists(t, "ui/"+m.UI.Entry)
	wantRuns := map[string]string{
		"edit_dates": "event:text:false action:select:true date:text:false start_at:text:false end_at:text:false shift_days:number:false " +
			"url:url:false kind:select:false name:text:false title:text:false venue_name:text:false description:text:false submission:text:false",
		"scheduled_import": "",
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
	exists(t, "migrations/003_recurring.sql")
	exists(t, "migrations/004_submissions.sql")
	exists(t, "widgets/event-details/widget.html")
	exists(t, "widgets/event-card/widget.html")
	exists(t, "partials/event_details.html")
}

// The scheduled import is the plugin's one standing run: it takes no inputs,
// is started by the import cron job and may only create calendar events.
func TestManifestScheduledImport(t *testing.T) {
	m, _ := loadManifest(t)
	if len(m.Runs) == 0 || m.Runs[0].Name != "edit_dates" {
		t.Fatalf("the first run must be edit_dates (the only one a page-started run reaches): %+v", m.Runs)
	}
	if r := m.Runs[0]; r.StandingApproval != nil || r.Cron != "" {
		t.Errorf("edit_dates has a standing approval or cron job: %+v", r)
	}
	standing := 0
	for _, r := range m.Runs {
		if r.StandingApproval == nil {
			continue
		}
		standing++
		sa := r.StandingApproval
		if r.Name != "scheduled_import" || r.Cron != "import" || len(r.Inputs) != 0 {
			t.Errorf("standing run %+v", r)
		}
		wantSchemas := []string{"calendar-event"}
		if !slices.Equal(sa.Schemas, wantSchemas) || !slices.Equal(sa.Create.Schemas, wantSchemas) || sa.Create.MaxPerHour != 100 ||
			!slices.Equal(sa.Create.LinkFields, []string{"ticket_url", "online_url", "website"}) {
			t.Errorf("standing approval %+v", sa)
		}
	}
	if standing != 1 {
		t.Errorf("%d runs with a standing approval, want 1", standing)
	}
	if len(m.AllowedHosts) == 0 || m.AllowedHosts[0] != "nominatim.openstreetmap.org" || !slices.Contains(m.AllowedHosts, "www.eventbriteapi.com") || len(m.AllowedHosts)-1 > 32 {
		t.Errorf("allowed_hosts = %v", m.AllowedHosts)
	}
	for _, key := range []string{"eventbrite_token", "import_sources"} {
		if _, ok := m.Settings.Properties[key]; !ok {
			t.Errorf("setting %s missing", key)
		}
	}
	if tok := m.Settings.Properties["eventbrite_token"]; !tok.WriteOnly {
		t.Errorf("eventbrite_token must be writeOnly (sealed): %+v", tok)
	}
}

// TestManifestSDKValidate runs the manifest through the validation core
// applies at load, so a manifest core would refuse fails the build.
func TestManifestSDKValidate(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if err := m.Validate(); err != nil {
		t.Errorf("core would refuse this manifest: %v", err)
	}
	if len(m.Runs) == 0 {
		t.Error("the manifest decoded without runs")
	}
}

func TestImportSourceURLPatternAllowsAnEmptyAddress(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Settings struct {
			Properties struct {
				Sources struct {
					Items struct {
						Properties struct {
							URL struct {
								Pattern string `json:"pattern"`
							} `json:"url"`
						} `json:"properties"`
					} `json:"items"`
				} `json:"import_sources"`
			} `json:"properties"`
		} `json:"settings_schema"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	re, err := regexp.Compile(m.Settings.Properties.Sources.Items.Properties.URL.Pattern)
	if err != nil {
		t.Fatal(err)
	}
	for in, want := range map[string]bool{"": true, "https://x.example/a.ics": true, "webcal://x.example/a.ics": true, "http://x.example": false, "ftp://x": false} {
		if re.MatchString(in) != want {
			t.Errorf("address %q: match %v, want %v", in, !want, want)
		}
	}
}
