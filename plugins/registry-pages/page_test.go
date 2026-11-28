package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func directoryHost(t *testing.T) *fakeHost {
	t.Helper()
	h := newFakeHost(t)
	h.publish(registryURL, 24*time.Hour,
		entry{Kind: kindPlugin, Name: "tagger", Title: "Tagger", Tier: tierFirstParty, Permissions: []string{"kv"}},
		entry{Kind: kindPlugin, Name: "mailer", Tier: tierCommunity, Author: "acme"},
		entry{Kind: kindTheme, Name: "boreal", Title: "Boreal", Tier: tierFirstParty, Screenshot: "screenshots/boreal.png"},
	)
	return h
}

func placeholder(path string) sdk.PageContext {
	return sdk.PageContext{Path: path, Blocks: []sdk.Block{
		{ID: "intro", Type: "text", Title: "Intro"},
		{ID: "dir", Type: "text", Title: "Directory", Fields: map[string]any{"anchor": "registry-directory"}},
		{ID: "outro", Type: "text"},
	}}
}

func detailPage(path string) sdk.PageContext {
	return sdk.PageContext{Path: path, Entity: &sdk.EntitySummary{SchemaSlug: "registry-entry", Slug: "x"}, Blocks: []sdk.Block{{ID: "body", Type: "text"}}}
}

func run(t *testing.T, p sdk.PageContext) sdk.PageContext {
	t.Helper()
	out, err := pageFilter(p)
	if err != nil {
		t.Fatalf("pageFilter error: %v", err)
	}
	return out
}

func TestDirectoryReplacesPlaceholderByAnchor(t *testing.T) {
	directoryHost(t)
	out := run(t, placeholder("/plugins"))
	if len(out.Blocks) != 3 || out.Blocks[0].ID != "intro" || out.Blocks[2].ID != "outro" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	b := out.Blocks[1]
	if b.ID != "dir" || b.Title != "Directory" || b.Type != "registry_directory" {
		t.Fatalf("block %+v", b)
	}
	if b.Content["state"] != stateOK || b.Content["linked"] != true || b.Content["list_url"] != "/plugins" {
		t.Fatalf("content %+v", b.Content)
	}
	cards := b.Content["cards"].([]pluginCard)
	if len(cards) != 2 || cards[0].Name != "tagger" || cards[1].Name != "mailer" {
		t.Fatalf("cards %+v", cards)
	}
}

func TestDirectoryThemes(t *testing.T) {
	directoryHost(t)
	b := run(t, placeholder("/themes")).Blocks[1]
	cards := b.Content["cards"].([]themeCard)
	if b.Content["list_url"] != "/themes" || len(cards) != 1 || cards[0].Name != "boreal" || cards[0].Screenshot != "https://hatamoto.dev/screenshots/boreal.png" {
		t.Fatalf("content %+v", b.Content)
	}
}

func TestDirectoryPlaceholderByType(t *testing.T) {
	directoryHost(t)
	p := sdk.PageContext{Path: "/plugins", Blocks: []sdk.Block{{ID: "a", Type: "text"}, {ID: "b", Type: "registry_directory"}}}
	out := run(t, p)
	if out.Blocks[0].Type != "text" || out.Blocks[1].Content["state"] != stateOK {
		t.Fatalf("blocks %+v", out.Blocks)
	}
}

func TestDirectoryWithoutPlaceholderIsUntouched(t *testing.T) {
	h := directoryHost(t)
	p := sdk.PageContext{Path: "/plugins", Blocks: []sdk.Block{{ID: "a", Type: "text"}}}
	out := run(t, p)
	if len(out.Blocks) != 1 || out.Blocks[0].Content != nil || len(h.calls) != 0 {
		t.Fatalf("blocks %+v, calls %v", out.Blocks, h.calls)
	}
}

func TestDirectoryDoesNotMutateTheInput(t *testing.T) {
	directoryHost(t)
	p := placeholder("/plugins")
	run(t, p)
	if p.Blocks[1].Type != "text" || p.Blocks[1].Content != nil {
		t.Fatalf("input changed: %+v", p.Blocks[1])
	}
}

func TestDirectoryUnavailableIsABlockNotAnError(t *testing.T) {
	h := newFakeHost(t)
	h.fail(registryURL)
	b := run(t, placeholder("/themes")).Blocks[1]
	if b.Type != "registry_directory" || b.Content["state"] != stateUnavailable {
		t.Fatalf("block %+v", b)
	}
}

func TestOtherPathsMakeNoHostCall(t *testing.T) {
	h := directoryHost(t)
	for _, path := range []string{"/", "/pluginsx", "/plugins/a/b", "/plugins/", "/blog/post", "/themes/Ab", "/plugins/a"} {
		p := detailPage(path)
		out := run(t, p)
		if len(out.Blocks) != 1 {
			t.Errorf("%s: blocks %+v", path, out.Blocks)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("host calls %v", h.calls)
	}
}

func TestDetailPlugin(t *testing.T) {
	directoryHost(t)
	out := run(t, detailPage("/plugins/tagger"))
	if len(out.Blocks) != 2 || out.Blocks[0].ID != "body" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	b := out.Blocks[1]
	c := b.Content
	if b.Type != "registry_detail" || b.ID != "registry-detail" || c["type"] != "plugin" || c["list_name"] != "Plugins" || c["list_url"] != "/plugins" {
		t.Fatalf("block %+v", b)
	}
	if card := c["plugin"].(pluginCard); card.Name != "tagger" || card.PermCount != 1 {
		t.Fatalf("card %+v", card)
	}
	if _, ok := c["theme"]; ok {
		t.Error("plugin detail carries a theme")
	}
}

func TestDetailTheme(t *testing.T) {
	directoryHost(t)
	c := run(t, detailPage("/themes/boreal")).Blocks[1].Content
	if c["type"] != "theme" || c["list_name"] != "Themes" || c["list_url"] != "/themes" || c["theme"].(themeCard).Name != "boreal" {
		t.Fatalf("content %+v", c)
	}
}

func TestDetailUnknownOrWrongKindGetsNoBlock(t *testing.T) {
	directoryHost(t)
	for _, path := range []string{"/themes/tagger", "/plugins/boreal", "/plugins/nope", "/themes/nope"} {
		if out := run(t, detailPage(path)); len(out.Blocks) != 1 {
			t.Errorf("%s: blocks %+v", path, out.Blocks)
		}
	}
}

func TestDetailNeedsTheRegistryEntryEntity(t *testing.T) {
	h := directoryHost(t)
	nilEntity := detailPage("/plugins/tagger")
	nilEntity.Entity = nil
	other := detailPage("/plugins/tagger")
	other.Entity.SchemaSlug = "page"
	for name, p := range map[string]sdk.PageContext{"nil": nilEntity, "other": other} {
		if out := run(t, p); len(out.Blocks) != 1 {
			t.Errorf("%s: blocks %+v", name, out.Blocks)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("host calls %v", h.calls)
	}
}

func TestDetailUnavailableGetsNoBlock(t *testing.T) {
	h := newFakeHost(t)
	h.fail(registryURL)
	if out := run(t, detailPage("/plugins/tagger")); len(out.Blocks) != 1 {
		t.Fatalf("blocks %+v", out.Blocks)
	}
}

func TestDetailReplacesAnExistingBlock(t *testing.T) {
	directoryHost(t)
	p := detailPage("/plugins/tagger")
	p.Blocks = []sdk.Block{{ID: "x", Type: "registry_detail", Title: "T"}, {ID: "y", Type: "text", Fields: map[string]any{"anchor": "registry-detail"}}}
	out := run(t, p)
	if len(out.Blocks) != 2 || out.Blocks[0].ID != "x" || out.Blocks[0].Title != "T" || out.Blocks[0].Content["type"] != "plugin" || out.Blocks[1].Content != nil {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	p.Blocks = p.Blocks[1:]
	out = run(t, p)
	if len(out.Blocks) != 1 || out.Blocks[0].ID != "y" || out.Blocks[0].Type != "registry_detail" {
		t.Fatalf("anchor block %+v", out.Blocks)
	}
}

func TestFilterFailsOpenOnPanic(t *testing.T) {
	directoryHost(t)
	mem = snapshot{}
	callHost = nil // a nil seam panics inside views; views itself recovers
	p := placeholder("/plugins")
	out, err := pageFilter(p)
	if err != nil {
		t.Fatalf("error %v", err)
	}
	if out.Blocks[1].Content["state"] != stateUnavailable {
		t.Fatalf("block %+v", out.Blocks[1])
	}
}

func TestDetailPathPattern(t *testing.T) {
	cases := map[string]bool{
		"/plugins/ab": true, "/themes/ab": true, "/plugins/a": false, "/plugins/Ab": false, "/plugins/ab/": false,
		"/plugins/" + strings.Repeat("a", 40): true, "/plugins/" + strings.Repeat("a", 41): false,
		"/plugins/1ab": false, "/other/ab": false, "/plugins": false,
	}
	for path, want := range cases {
		if got := detailPathRe.MatchString(path); got != want {
			t.Errorf("%s: %v, want %v", path, got, want)
		}
	}
}

// The theme reads snake_case keys: check them on the JSON the host receives.
func TestContentJSONKeys(t *testing.T) {
	directoryHost(t)
	raw, err := json.Marshal(run(t, placeholder("/plugins")))
	if err != nil {
		t.Fatal(err)
	}
	var back struct {
		Blocks []struct {
			Content struct {
				State   string           `json:"state"`
				Linked  bool             `json:"linked"`
				ListURL string           `json:"list_url"`
				Cards   []map[string]any `json:"cards"`
			} `json:"content"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(raw, &back); err != nil {
		t.Fatal(err)
	}
	c := back.Blocks[1].Content
	if c.State != "ok" || !c.Linked || c.ListURL != "/plugins" || len(c.Cards) != 2 {
		t.Fatalf("content %+v", c)
	}
	var keys []string
	for k := range c.Cards[0] {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if want := strings.Fields("about author description features name perm_count perm_lines perm_warn permissions search source tier tier_label title version"); !slices.Equal(keys, want) {
		t.Fatalf("card keys %v", keys)
	}

	raw, _ = json.Marshal(run(t, detailPage("/themes/boreal")))
	for _, key := range []string{`"type":"theme"`, `"list_name":"Themes"`, `"list_url":"/themes"`, `"theme":{`} {
		if !strings.Contains(string(raw), key) {
			t.Errorf("detail JSON lacks %s: %s", key, raw)
		}
	}
}
