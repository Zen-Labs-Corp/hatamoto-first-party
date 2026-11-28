package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func TestCardsSortedPluginsOnly(t *testing.T) {
	pd, _ := buildDirs(&index{Packages: []entry{
		{Kind: kindPlugin, Name: "zeta", Version: "v1.0.0", Tier: tierFirstParty, Author: "hidden", Source: "https://example.org/zeta", Permissions: []string{"db", "kv"}},
		{Kind: kindTheme, Name: "aaa-theme", Version: "1.0.0"},
		{Kind: kindKit, Name: "a-kit", Version: "1.0.0"},
		{Kind: kindPlugin, Name: "alpha", Version: "2.0.0", Title: "Alpha", Description: "Does <b>things</b>", Tier: tierCommunity, Author: "acme", Source: "http://example.org/alpha"},
	}})
	if pd.State != stateOK || len(pd.Cards) != 2 {
		t.Fatalf("view = %+v", pd)
	}
	z, a := pd.Cards[0], pd.Cards[1] // first-party first
	if a.Name != "alpha" || z.Name != "zeta" {
		t.Fatalf("order: %s, %s", a.Name, z.Name)
	}
	if a.Version != "v2.0.0" || z.Version != "v1.0.0" {
		t.Errorf("versions %q %q", a.Version, z.Version)
	}
	if a.TierLabel != "Community" || a.Author != "acme" || z.TierLabel != "First-party" || z.Author != "" {
		t.Errorf("tier/author: %+v %+v", a, z)
	}
	if a.Source != "" || z.Source != "https://example.org/zeta" {
		t.Errorf("source: %q %q", a.Source, z.Source)
	}
	if a.Search != "alpha alpha does <b>things</b>" {
		t.Errorf("search %q", a.Search)
	}
}

func TestCardsSortWithinTier(t *testing.T) {
	pd, _ := buildDirs(&index{Packages: []entry{
		{Kind: kindPlugin, Name: "dev-a", Tier: tierDeveloper, Author: "Ann"},
		{Kind: kindPlugin, Name: "comm-b", Tier: tierCommunity},
		{Kind: kindPlugin, Name: "first-b", Tier: tierFirstParty},
		{Kind: kindPlugin, Name: "first-a", Tier: tierFirstParty},
		{Kind: kindPlugin, Name: "untiered"},
		{Kind: kindPlugin, Name: "comm-a", Tier: tierCommunity},
	}})
	var got []string
	for _, c := range pd.Cards {
		got = append(got, c.Name)
	}
	if want := []string{"first-a", "first-b", "comm-a", "comm-b", "dev-a", "untiered"}; !slices.Equal(got, want) {
		t.Fatalf("order %v, want %v", got, want)
	}
	if dev := pd.Cards[4]; dev.Search != " dev-a  ann" || dev.TierLabel != "Developer" || dev.Author != "Ann" {
		t.Errorf("developer card %+v", dev)
	}
	if u := pd.Cards[5]; u.Tier != "" || u.TierLabel != "" || u.Author != "" {
		t.Errorf("untiered card %+v", u)
	}
}

func TestCardsSplitKinds(t *testing.T) {
	pd, td := buildDirs(&index{Packages: []entry{
		{Kind: kindPlugin, Name: "zeta", Version: "1.0.0", Tier: tierFirstParty},
		{Kind: kindTheme, Name: "zen", Version: "1.0.0", Title: "Zen", Description: "Calm", Tier: tierCommunity, Author: "Acme", Source: "https://example.org/zen", Screenshot: "screenshots/zen.webp", About: "one\ntwo", Features: []string{"f"}},
		{Kind: kindTheme, Name: "aqua", Version: "v2.0.0", Tier: tierFirstParty, Author: "hidden", Source: "http://example.org/aqua"},
	}})
	if len(pd.Cards) != 1 || pd.Cards[0].Name != "zeta" {
		t.Fatalf("plugins = %+v", pd)
	}
	if td.State != stateOK || len(td.Cards) != 2 {
		t.Fatalf("themes = %+v", td)
	}
	a, z := td.Cards[0], td.Cards[1]
	if a.Name != "aqua" || a.Version != "v2.0.0" || a.TierLabel != "First-party" || a.Author != "" || a.Source != "" || a.Screenshot != "" {
		t.Errorf("aqua = %+v", a)
	}
	if z.TierLabel != "Community" || z.Author != "Acme" || z.Source != "https://example.org/zen" || z.Search != "zen zen calm acme" {
		t.Errorf("zen = %+v", z)
	}
	if z.Screenshot != "https://hatamoto.dev/screenshots/zen.webp" {
		t.Errorf("screenshot %q", z.Screenshot)
	}
	if !slices.Equal(z.About, []string{"one", "two"}) || !slices.Equal(z.Features, []string{"f"}) {
		t.Errorf("about/features %+v", z)
	}
}

func TestCardsEmptyIndex(t *testing.T) {
	pd, td := buildDirs(&index{})
	if pd.State != stateEmpty || td.State != stateEmpty || len(pd.Cards) != 0 || len(td.Cards) != 0 {
		t.Fatalf("%+v %+v", pd, td)
	}
}

func TestCardsPermissionCard(t *testing.T) {
	pd, _ := buildDirs(&index{Packages: []entry{
		{Kind: kindPlugin, Name: "tags", Version: "1.0.0", Permissions: []string{"settings", "site.scripts", "http.fetch.any", "future.perm"}},
		{Kind: kindPlugin, Name: "plain", Version: "1.0.0"},
	}})
	plain, tags := pd.Cards[0], pd.Cards[1]
	if plain.PermCount != 0 || len(plain.PermWarn) != 0 || len(plain.PermLines) != 0 {
		t.Errorf("plain = %+v", plain)
	}
	if tags.PermCount != 4 || len(tags.PermLines) != 4 {
		t.Fatalf("tags = %+v", tags)
	}
	if got := strings.Join(tags.Permissions, ","); got != "settings,site.scripts,http.fetch.any,future.perm" {
		t.Errorf("Permissions = %q", got)
	}
	if got := strings.Join(tags.PermWarn, "|"); got != "JavaScript on every public page.|Any web address." {
		t.Errorf("warn badges = %q", got)
	}
	if l := tags.PermLines[1]; !l.Warn || l.Name != "site.scripts" {
		t.Errorf("line = %+v", l)
	}
	if l := tags.PermLines[2]; !l.Warn || strings.HasPrefix(l.Text, "WARNING:") || !strings.HasPrefix(l.Text, "send requests") {
		t.Errorf("WARNING prefix not stripped: %+v", l)
	}
	if l := tags.PermLines[3]; l.Text != "future.perm" || l.Warn {
		t.Errorf("unknown permission line = %+v", l)
	}
}

func TestCardsDeveloperScreenshotUsesDeveloperURL(t *testing.T) {
	_, td := buildDirs(&index{Packages: []entry{
		{Kind: kindTheme, Name: "main", Tier: tierFirstParty, Screenshot: "screenshots/main.png"},
		{Kind: kindTheme, Name: "dev", Tier: tierDeveloper, Screenshot: "screenshots/dev.png"},
	}})
	if got := td.Cards[0].Screenshot; got != "https://hatamoto.dev/screenshots/main.png" {
		t.Errorf("main screenshot %q", got)
	}
	if got := td.Cards[1].Screenshot; got != "https://hatamoto.dev/p/devportal/screenshots/dev.png" {
		t.Errorf("developer screenshot %q", got)
	}
}

func TestSafeHTTPS(t *testing.T) {
	for in, want := range map[string]string{
		"https://example.org/x":       "https://example.org/x",
		" https://example.org/x ":     "https://example.org/x",
		"http://example.org/x":        "",
		"javascript:alert(1)":         "",
		"https://user:pw@example.org": "",
		"https:///nohost":             "",
		"":                            "",
	} {
		if got := safeHTTPS(in); got != want {
			t.Errorf("safeHTTPS(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestDisplayVersion(t *testing.T) {
	for in, want := range map[string]string{"1.2.0": "v1.2.0", "v1.2.0": "v1.2.0", "": ""} {
		if got := displayVersion(in); got != want {
			t.Errorf("displayVersion(%q) = %q", in, got)
		}
	}
}

// The theme reads these exact keys.
func TestCardJSONKeys(t *testing.T) {
	keys := func(v any) []string {
		raw, err := json.Marshal(v)
		if err != nil {
			t.Fatal(err)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(raw, &m); err != nil {
			t.Fatal(err)
		}
		var out []string
		for k := range m {
			out = append(out, k)
		}
		slices.Sort(out)
		return out
	}
	want := func(s string) []string {
		out := strings.Fields(s)
		slices.Sort(out)
		return out
	}
	if got, w := keys(pluginCard{}), want("name title version tier tier_label author description source perm_count perm_warn perm_lines permissions search about features"); !slices.Equal(got, w) {
		t.Errorf("plugin card keys %v", got)
	}
	if got, w := keys(themeCard{}), want("name title version description source screenshot tier tier_label author search about features"); !slices.Equal(got, w) {
		t.Errorf("theme card keys %v", got)
	}
	if got, w := keys(permLine{}), want("name text warn"); !slices.Equal(got, w) {
		t.Errorf("perm line keys %v", got)
	}
	if got, w := keys(pluginDir{}), want("state cards"); !slices.Equal(got, w) {
		t.Errorf("dir keys %v", got)
	}
}
