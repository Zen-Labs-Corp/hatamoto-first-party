package main

import (
	"encoding/json"
	"net/http"
	"os"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func loadManifest(t *testing.T) sdk.Manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m sdk.Manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func rawManifest(t *testing.T) []byte {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestManifestIsValidForTheSDK(t *testing.T) {
	m := loadManifest(t)
	if err := m.WithDefaults().Validate(); err != nil {
		t.Fatal(err)
	}
	if ok, err := m.Compatible(); !ok || err != nil {
		t.Fatalf("Compatible() = %v, %v", ok, err)
	}
	if m.Name != "hn" || m.SDK != "^0.29" || m.Version != "1.2.0" {
		t.Fatalf("name %q sdk %q version %q", m.Name, m.SDK, m.Version)
	}
}

func TestManifestSDKMatchesGoMod(t *testing.T) {
	mod, err := os.ReadFile("go.mod")
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`hatamoto\.dev/sdk v0\.(\d+)\.\d+(-rc\.\d+)?`).FindSubmatch(mod)
	if m == nil {
		t.Fatal("go.mod has no hatamoto.dev/sdk requirement")
	}
	if want := "^0." + string(m[1]); loadManifest(t).SDK != want {
		t.Fatalf("plugin.json sdk %q, go.mod requires %q", loadManifest(t).SDK, want)
	}
}

func TestManifestPermissions(t *testing.T) {
	got := slices.Clone(loadManifest(t).Permissions)
	slices.Sort(got)
	want := []string{"client.key", "comments.identity", "comments.moderate", "comments.read", "db", "entities.write.own", "settings", "site.scripts"}
	if !slices.Equal(got, want) {
		t.Fatalf("permissions %v, want %v", got, want)
	}
	raw := string(rawManifest(t))
	for _, bad := range []string{"writeOnly", "http.fetch", `"kv"`, "mail.send", "entities.read", `"cookies"`, `"funcs"`} {
		if strings.Contains(raw, bad) {
			t.Errorf("plugin.json contains %q", bad)
		}
	}
}

// The new 0.27 fields are decoded from the raw JSON, so the test does not depend
// on which of them the SDK struct knows.
func TestManifestEntityPagesAndQuery(t *testing.T) {
	var doc struct {
		EntitySchema     string   `json:"entity_schema"`
		EntityFields     []string `json:"entity_fields"`
		PagePaths        []string `json:"page_paths"`
		PagePathPrefixes []string `json:"page_path_prefixes"`
		PageQuery        []string `json:"page_query"`
		RequestHeaders   []string `json:"request_headers"`
	}
	if err := json.Unmarshal(rawManifest(t), &doc); err != nil {
		t.Fatal(err)
	}
	if doc.EntitySchema != "hn-story" {
		t.Errorf("entity_schema %q", doc.EntitySchema)
	}
	if want := []string{"url", "text", "kind"}; !slices.Equal(doc.EntityFields, want) {
		t.Errorf("entity_fields %v", doc.EntityFields)
	}
	wantPaths := []string{"/", "/news", "/newest", "/front", "/newcomments", "/ask", "/show", "/jobs", "/submit", "/threads", "/user", "/from", "/favorites", "/login", "/search", "/reply"}
	if !slices.Equal(doc.PagePaths, wantPaths) || len(doc.PagePaths) != 16 {
		t.Errorf("page_paths %v", doc.PagePaths)
	}
	seen := map[string]bool{}
	for _, p := range doc.PagePaths {
		if seen[p] {
			t.Errorf("page path %q twice", p)
		}
		seen[p] = true
		if strings.ContainsAny(p, "?#*") {
			t.Errorf("page path %q has a query, fragment or wildcard", p)
		}
	}
	if want := []string{"/item/"}; !slices.Equal(doc.PagePathPrefixes, want) {
		t.Errorf("page_path_prefixes %v", doc.PagePathPrefixes)
	}
	if want := []string{"p", "id", "day", "site", "kind", "next", "view", "q", "m"}; !slices.Equal(doc.PageQuery, want) {
		t.Errorf("page_query %v", doc.PageQuery)
	}
	if want := []string{"sec-fetch-site"}; !slices.Equal(doc.RequestHeaders, want) {
		t.Errorf("request_headers %v", doc.RequestHeaders)
	}
}

// routeOwners is every manifest route with the phase that implements it and its
// public flag. wiring_test.go checks registered routes against it.
var routeOwners = map[string]struct {
	phase  string
	public bool
}{
	"POST /username":                           {"accounts", true},
	"POST /profile":                            {"accounts", true},
	"POST /login":                              {"accounts", true},
	"POST /signup":                             {"accounts", true},
	"POST /logout":                             {"accounts", true},
	"POST /logout/all":                         {"accounts", true},
	"POST /password":                           {"accounts", true},
	"POST /password/connect":                   {"accounts", true},
	"POST /submit":                             {"stories", true},
	"POST /story/{id}/edit":                    {"stories", true},
	"POST /story/{id}/delete":                  {"stories", true},
	"POST /vote/story/{id}":                    {"stories", true},
	"POST /admin/jobs":                         {"stories", false},
	"POST /vote/comment/{id}":                  {"comments", true},
	"POST /flag/{kind}/{id}":                   {"moderation", true},
	"POST /vouch/{kind}/{id}":                  {"moderation", true},
	"GET /admin/queue":                         {"moderation", false},
	"POST /admin/users/{name}/ban":             {"moderation", false},
	"POST /admin/users/{name}/unban":           {"moderation", false},
	"POST /admin/users/{name}/password/remove": {"moderation", false},
	"POST /admin/domains":                      {"moderation", false},
	"POST /admin/domains/remove":               {"moderation", false},
	"POST /admin/nets/ban":                     {"moderation", false},
	"POST /admin/nets/unban":                   {"moderation", false},
	"GET /admin/log":                           {"moderation", false},
	"POST /admin/items/kill":                   {"moderation", false},
	"POST /admin/items/restore":                {"moderation", false},
	"POST /favorite/{kind}/{id}":               {"parity", true},
	"POST /unfavorite/{kind}/{id}":             {"parity", true},
	"POST /hide/{id}":                          {"parity", true},
	"POST /unhide/{id}":                        {"parity", true},
	"GET /rss":                                 {"parity", true},
	"GET /api/feed/{name}":                     {"parity", true},
	"GET /api/item/{id}":                       {"parity", true},
	"GET /api/user/{name}":                     {"parity", true},
	"GET /state/{entity_id}":                   {"polish", true},
}

func TestManifestRoutes(t *testing.T) {
	m := loadManifest(t)
	if len(m.Routes) != 36 || len(routeOwners) != 36 {
		t.Fatalf("%d routes, %d owners, want 36", len(m.Routes), len(routeOwners))
	}
	public, admin := http.NewServeMux(), http.NewServeMux()
	register := func(mux *http.ServeMux, key string) {
		defer func() {
			if r := recover(); r != nil {
				t.Errorf("route %s does not register: %v", key, r)
			}
		}()
		mux.HandleFunc(key, func(http.ResponseWriter, *http.Request) {})
	}
	for _, r := range m.Routes {
		key := r.Method + " " + r.Path
		owner, ok := routeOwners[key]
		if !ok {
			t.Errorf("unexpected route %s", key)
			continue
		}
		if r.Public != owner.public {
			t.Errorf("route %s: public = %v, want %v", key, r.Public, owner.public)
		}
		if len(r.Roles) != 0 {
			t.Errorf("route %s: roles %v", key, r.Roles)
		}
		if r.Public {
			register(public, key)
		} else {
			register(admin, key)
		}
	}
}

func TestManifestCronUILimits(t *testing.T) {
	m := loadManifest(t)
	want := []sdk.CronJob{{Name: "rank", Every: "1m"}, {Name: "comment_rank", Every: "1m"}, {Name: "reconcile", Every: "1h"}, {Name: "sweep", Every: "1h"}, {Name: "search", Every: "5m"}}
	if !slices.Equal(m.Cron, want) {
		t.Errorf("cron %+v", m.Cron)
	}
	if m.UI == nil || m.UI.Title != "News moderation" || m.UI.Entry != "index.html" || !slices.Equal(m.UI.Roles, []string{"owner"}) {
		t.Errorf("ui %+v", m.UI)
	}
	if m.Limits.MemoryPages != 256 || m.Limits.MaxInstances != 4 {
		t.Errorf("limits %+v", m.Limits)
	}
}

func TestManifestSettingsSchema(t *testing.T) {
	var doc struct {
		Settings struct {
			Properties map[string]struct {
				Type      string `json:"type"`
				Default   any    `json:"default"`
				WriteOnly bool   `json:"writeOnly"`
			} `json:"properties"`
		} `json:"settings_schema"`
	}
	if err := json.Unmarshal(rawManifest(t), &doc); err != nil {
		t.Fatal(err)
	}
	want := map[string]any{"flag_karma": 30.0, "downvote_karma": 500.0, "vouch_karma": 31.0, "edit_window_minutes": 120.0, "submit_per_hour": 6.0, "flags_to_dead": 5.0, "page_size": 30.0, "pinned_items": ""}
	if len(doc.Settings.Properties) != len(want) {
		t.Fatalf("%d settings, want %d", len(doc.Settings.Properties), len(want))
	}
	for k, def := range want {
		p, ok := doc.Settings.Properties[k]
		typ := "integer"
		if _, isStr := def.(string); isStr {
			typ = "string"
		}
		if !ok || p.Type != typ || p.Default != def || p.WriteOnly {
			t.Errorf("setting %s = %+v, want %s default %v", k, p, typ, def)
		}
	}
}

// The hooks that run on every page view or comment get a short, explicit timeout.
func TestManifestHookTimeouts(t *testing.T) {
	m := loadManifest(t)
	for _, hook := range []string{sdk.RenderPage.Name, sdk.CommentCheckF.Name, sdk.CommentDraftF.Name} {
		if got := m.Limits.HookTimeoutsMS[hook]; got != 2000 {
			t.Errorf("hook_timeouts_ms[%q] = %d, want 2000", hook, got)
		}
	}
}
