package main

import (
	"fmt"
	"slices"
	"testing"
)

func TestPickTargetsDefault(t *testing.T) {
	var ents []entity
	for i := 1; i <= 30; i++ {
		ents = append(ents, entity{Path: fmt.Sprintf("/p%d", i), PublishedAt: fmt.Sprintf("2026-01-%02dT10:00:00Z", i)})
	}
	ents = append(ents, entity{Path: "/p30", PublishedAt: "2026-01-01T00:00:00Z"}, entity{Path: "/", PublishedAt: "2026-01-31T00:00:00Z"}, entity{})
	got := pickTargets(settings{}, ents)
	if len(got) != 25 || got[0] != "/" {
		t.Fatalf("got %d targets, first %q", len(got), got[0])
	}
	if got[1] != "/p30" || got[2] != "/p29" || got[24] != "/p7" {
		t.Fatalf("not newest first: %v", got)
	}
	seen := map[string]bool{}
	for _, p := range got {
		if seen[p] {
			t.Fatalf("duplicate %q", p)
		}
		seen[p] = true
	}
}

func TestPickTargetsConfigured(t *testing.T) {
	got := pickTargets(settings{URLs: []string{"/a", "/b"}}, []entity{{Path: "/x"}})
	if !slices.Equal(got, []string{"/a", "/b"}) {
		t.Fatalf("got %v", got)
	}
}

func TestSiteOrigin(t *testing.T) {
	if o, ok := siteOrigin(settings{PublicURL: "https://a.example"}, []entity{{Path: "/x", URL: "https://b.example/x"}}); !ok || o != "https://a.example" {
		t.Fatalf("setting wins: %q %v", o, ok)
	}
	ents := []entity{{Path: "/x", URL: "/x"}, {Path: "/y", URL: "https://b.example/y"}}
	if o, ok := siteOrigin(settings{}, ents); !ok || o != "https://b.example" {
		t.Fatalf("entity origin: %q %v", o, ok)
	}
	if _, ok := siteOrigin(settings{}, ents[:1]); ok {
		t.Fatal("relative url gives no origin")
	}
}

func fakeSettings(t *testing.T, raw string, calls *[]string) {
	t.Helper()
	callHost = func(fn string, _, out any) error {
		*calls = append(*calls, fn)
		switch fn {
		case "settings.get":
			return jsonInto(raw, out)
		case "entities.list":
			return jsonInto(`{"items":[{"path":"/n","url":"https://s.example/n","published_at":"2026-01-01T00:00:00Z"}],"next_cursor":""}`, out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func TestLoadSettingsNormalizes(t *testing.T) {
	var calls []string
	fakeSettings(t, `{"api_key":" k ","urls":["/a "," /a","b","/c"],"strategy":"weird","public_url":"https://x.example/"}`, &calls)
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	if s.APIKey != "k" || !slices.Equal(s.URLs, []string{"/a", "/c"}) || s.Strategy != "both" || s.PublicURL != "https://x.example" {
		t.Fatalf("settings = %+v", s)
	}
	if !slices.Equal(s.strategies(), []string{"mobile", "desktop"}) {
		t.Fatalf("strategies = %v", s.strategies())
	}
}

func TestResolveTargetsSkipsListWhenSettingsAnswer(t *testing.T) {
	var calls []string
	fakeSettings(t, `{"urls":["/a"],"public_url":"https://x.example"}`, &calls)
	s, _ := loadSettings()
	targets, origin, ok, err := resolveTargets(s)
	if err != nil || !ok || origin != "https://x.example" || !slices.Equal(targets, []string{"/a"}) {
		t.Fatalf("%v %q %v %v", targets, origin, ok, err)
	}
	if slices.Contains(calls, "entities.list") {
		t.Fatal("entities.list called although urls and public_url are set")
	}
}
