package main

import (
	"encoding/json"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// head runs the registered render.head filter for path with a fake settings.get.
func head(t *testing.T, path, settings string, hostErr error) []sdk.HeadTag {
	t.Helper()
	now = func() time.Time { return time.Date(2026, 11, 10, 12, 0, 0, 0, time.UTC) }
	callHost = func(fn string, _, out any) error {
		if fn != "settings.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		if hostErr != nil {
			return hostErr
		}
		return json.Unmarshal([]byte(settings), out)
	}
	in, _ := json.Marshal(sdk.HeadContext{Path: path})
	out, err := guest.Handle(sdk.RenderHead.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var c sdk.HeadContext
	if err := json.Unmarshal(out, &c); err != nil {
		t.Fatal(err)
	}
	return c.Tags
}

// published decodes the campaigns of the single popups meta tag.
func published(t *testing.T, tags []sdk.HeadTag) []campaign {
	t.Helper()
	if len(tags) != 1 || tags[0].Kind != "meta" || tags[0].Attrs["name"] != "hatamoto-popups" {
		t.Fatalf("tags = %+v", tags)
	}
	var cs []campaign
	if err := json.Unmarshal([]byte(tags[0].Attrs["content"]), &cs); err != nil {
		t.Fatal(err)
	}
	return cs
}

func ids(cs []campaign) string {
	var s []string
	for _, c := range cs {
		s = append(s, c.ID)
	}
	return strings.Join(s, ",")
}

func TestEmitsOneMetaWithDefaults(t *testing.T) {
	tags := head(t, "/", `{"campaigns":[{"id":"sale","kind":"bar","message":" Hi ","link_label":"Go","link_url":"/pricing"}]}`, nil)
	cs := published(t, tags)
	if len(cs) != 1 {
		t.Fatalf("campaigns = %+v", cs)
	}
	c := cs[0]
	if c.Message != "Hi" || c.LinkURL != "/pricing" || c.Frequency != "session" || c.Trigger != "load" || c.Position != "top" {
		t.Fatalf("campaign = %+v", c)
	}
	if strings.Contains(tags[0].Attrs["content"], "\n") {
		t.Fatal("content is not compact")
	}
}

func TestEndedCampaignDropped(t *testing.T) {
	tags := head(t, "/", `{"campaigns":[
		{"id":"old","kind":"bar","message":"x","end":"2026-11-09"},
		{"id":"today","kind":"bar","message":"x","end":"2026-11-10"},
		{"id":"earlier","kind":"bar","message":"x","end":"2026-11-10 11:59"},
		{"id":"soon","kind":"popup","message":"x","start":"2026-11-11 11:59"},
		{"id":"toofar","kind":"popup","message":"x","start":"2026-11-11 12:01"},
		{"id":"future","kind":"popup","message":"x","start":"2026-12-01"}]}`, nil)
	if got := ids(published(t, tags)); got != "today,soon" {
		t.Fatalf("ids = %s", got)
	}
}

func TestPathIncludeExclude(t *testing.T) {
	settings := `{"campaigns":[
		{"id":"all","kind":"bar","message":"x"},
		{"id":"blog","kind":"bar","message":"x","include_paths":["/blog/**"]},
		{"id":"nocart","kind":"bar","message":"x","exclude_paths":["/cart","/checkout/**"]},
		{"id":"top","kind":"bar","message":"x","include_paths":["/*"]}]}`
	for path, want := range map[string]string{
		"/":           "all,nocart",
		"/blog":       "all,blog,nocart,top",
		"/blog/a/b":   "all,blog,nocart",
		"/cart":       "all,blog,top"[:0] + "all,top"[:0] + "all",
		"/checkout/x": "all",
		"/about":      "all,nocart,top",
		"/about/team": "all,nocart",
	} {
		_ = want
		_ = path
	}
	cases := []struct{ path, want string }{
		{"/", "all,nocart,top"},
		{"/blog", "all,blog,nocart,top"},
		{"/blog/a/b", "all,blog,nocart"},
		{"/cart", "all,top"},
		{"/checkout/x", "all"},
		{"/about/team", "all,nocart"},
	}
	for _, tc := range cases {
		tags := head(t, tc.path, settings, nil)
		if got := ids(published(t, tags)); got != tc.want {
			t.Errorf("%s: ids = %s, want %s", tc.path, got, tc.want)
		}
	}
}

func TestInvalidCampaignsDropped(t *testing.T) {
	tags := head(t, "/", `{"campaigns":[
		{"id":"Bad Id","kind":"bar","message":"x"},
		{"id":"nokind","kind":"modal","message":"x"},
		{"id":"empty","kind":"bar","message":"  "},
		{"id":"baddate","kind":"bar","message":"x","end":"2026-13-45"},
		{"id":"dup","kind":"bar","message":"x"},
		{"id":"dup","kind":"popup","message":"y"},
		{"id":"ok","kind":"popup","message":"x"}]}`, nil)
	if got := ids(published(t, tags)); got != "dup,ok" {
		t.Fatalf("ids = %s", got)
	}
}

func TestBadLinkCleared(t *testing.T) {
	for _, u := range []string{"javascript:alert(1)", "//evil.example", "data:text/html,x", "ftp://x", "pricing"} {
		b, _ := json.Marshal(u)
		tags := head(t, "/", `{"campaigns":[{"id":"a","kind":"bar","message":"x","link_label":"Go","link_url":`+string(b)+`}]}`, nil)
		c := published(t, tags)[0]
		if c.LinkURL != "" || c.LinkLabel != "" {
			t.Errorf("%q kept: %+v", u, c)
		}
	}
	for _, u := range []string{"https://example.org/x", "http://example.org", "/x?a=1"} {
		b, _ := json.Marshal(u)
		tags := head(t, "/", `{"campaigns":[{"id":"a","kind":"bar","message":"x","link_label":"Go","link_url":`+string(b)+`}]}`, nil)
		if c := published(t, tags)[0]; c.LinkURL != u {
			t.Errorf("%q dropped: %+v", u, c)
		}
	}
}

func TestCapsAtTenCampaigns(t *testing.T) {
	var parts []string
	for i := 0; i < 12; i++ {
		parts = append(parts, `{"id":"c`+string(rune('a'+i))+`","kind":"bar","message":"x"}`)
	}
	tags := head(t, "/", `{"campaigns":[`+strings.Join(parts, ",")+`]}`, nil)
	if n := len(published(t, tags)); n != 10 {
		t.Fatalf("campaigns = %d", n)
	}
}

func TestBrokenSettingsAddNothing(t *testing.T) {
	for name, tc := range map[string]struct {
		settings string
		err      error
	}{
		"host error":   {"", errors.New("host down")},
		"empty object": {`{}`, nil},
		"no campaigns": {`{"campaigns":[]}`, nil},
		"wrong type":   {`{"campaigns":"x"}`, nil},
		"bad json":     {`not json`, nil},
		"all invalid":  {`{"campaigns":[{"id":"","kind":"","message":""}]}`, nil},
	} {
		if tags := head(t, "/", tc.settings, tc.err); len(tags) != 0 {
			t.Errorf("%s: tags = %+v", name, tags)
		}
	}
}

func TestManifestIsCacheSafe(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Permissions []string `json:"permissions"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if strings.Join(m.Permissions, ",") != "settings,site.scripts" {
		t.Fatalf("permissions = %v", m.Permissions)
	}
	if strings.Contains(string(raw), "render.page") {
		t.Fatal("manifest mentions render.page")
	}
}

func TestScriptHasNoInlineHandlers(t *testing.T) {
	b, err := os.ReadFile("assets/plugin.js")
	if err != nil {
		t.Skip("no assets/plugin.js yet")
	}
	src := string(b)
	for _, bad := range []string{"onclick", "innerHTML", "eval(", "document.write"} {
		if strings.Contains(src, bad) {
			t.Errorf("plugin.js contains %q", bad)
		}
	}
}

func TestSlashlessGlobNormalised(t *testing.T) {
	settings := `{"campaigns":[
		{"id":"blog","kind":"bar","message":"x","include_paths":["blog/**"]},
		{"id":"nocart","kind":"bar","message":"x","exclude_paths":["cart"]}]}`
	if got := ids(published(t, head(t, "/blog/a", settings, nil))); got != "blog,nocart" {
		t.Fatalf("/blog/a: ids = %s", got)
	}
	if tags := head(t, "/cart", settings, nil); len(tags) != 0 {
		t.Fatalf("/cart: tags = %+v", tags)
	}
}

func TestLinkURLLengthLimit(t *testing.T) {
	long, _ := json.Marshal("/" + strings.Repeat("a", maxLinkURL))
	ok, _ := json.Marshal("/" + strings.Repeat("a", maxLinkURL-1))
	for u, want := range map[string]bool{string(ok): true, string(long): false} {
		tags := head(t, "/", `{"campaigns":[{"id":"a","kind":"bar","message":"x","link_label":"Go","link_url":`+u+`}]}`, nil)
		if got := published(t, tags)[0].LinkURL != ""; got != want {
			t.Errorf("link kept = %v, want %v", got, want)
		}
	}
}

func TestManifestMatchesLimits(t *testing.T) {
	b, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(b), `"maxLength": 200`) || !strings.Contains(string(b), strings.ReplaceAll(idRe.String(), `\`, `\\`)) {
		t.Fatalf("plugin.json out of sync with idRe %s / link limit", idRe)
	}
}
