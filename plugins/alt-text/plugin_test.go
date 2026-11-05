package main

import (
	"testing"
)

func TestLoadSettingsLimitAndOnlyMissing(t *testing.T) {
	cases := []struct {
		set         map[string]any
		limit       float64
		onlyMissing bool
	}{
		{map[string]any{}, 50, true},
		{map[string]any{"limit": 0}, 50, true},
		{map[string]any{"limit": -3}, 50, true},
		{map[string]any{"limit": 7}, 7, true},
		{map[string]any{"limit": 7.0, "only_missing": false}, 7, false},
		{map[string]any{"limit": 1000, "only_missing": true}, 200, true},
		{map[string]any{"limit": 0.5}, 50, true},
	}
	for _, c := range cases {
		f := newFake(t)
		f.settings = c.set
		s, err := loadSettings()
		if err != nil || s.Limit != c.limit || s.onlyMissing() != c.onlyMissing {
			t.Errorf("settings %v: limit %v only_missing %v, want %v %v (%v)", c.set, s.Limit, s.onlyMissing(), c.limit, c.onlyMissing, err)
		}
	}
}

func TestAbsURL(t *testing.T) {
	cases := []struct{ url, site, want string }{
		{"/media/a.jpg", "https://example.com", "https://example.com/media/a.jpg"},
		{"media/a.jpg", "https://example.com", "https://example.com/media/a.jpg"},
		{"https://cdn.test/a.jpg", "", "https://cdn.test/a.jpg"},
		{"HTTP://cdn.test/a.jpg", "https://example.com", "HTTP://cdn.test/a.jpg"},
		{"", "https://example.com", ""},
	}
	for _, c := range cases {
		got, err := absURL(c.url, c.site)
		if err != nil || got != c.want {
			t.Errorf("absURL(%q,%q) = %q, %v; want %q", c.url, c.site, got, err, c.want)
		}
	}
	if _, err := absURL("/media/a.jpg", ""); err == nil {
		t.Error("relative URL without a site address must fail")
	}
}
