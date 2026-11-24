package core_test

import (
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestCleanTitle(t *testing.T) {
	for _, tc := range []struct {
		in, want string
		ok       bool
	}{
		{"", "", false},
		{"   ", "", false},
		{"  A   b\tc ", "A b c", true},
		{strings.Repeat("é", 80), strings.Repeat("é", 80), true},
		{strings.Repeat("é", 81), "", false},
	} {
		got, err := core.CleanTitle(tc.in)
		if (err == nil) != tc.ok || got != tc.want {
			t.Errorf("CleanTitle(%q) = %q, %v", tc.in, got, err)
		}
	}
}

func TestNormalizeURL(t *testing.T) {
	canon, key, domain, err := core.NormalizeURL("https://www.Example.com/a/?x=1#top")
	if err != nil || canon != "https://www.Example.com/a/?x=1" || key != "example.com/a?x=1" || domain != "example.com" {
		t.Errorf("got %q %q %q %v", canon, key, domain, err)
	}
	_, key, _, _ = core.NormalizeURL("https://www.Example.com/a/")
	if key != "example.com/a" {
		t.Errorf("key %q", key)
	}
	for _, u := range []string{"https://example.com./a", "https://www.example.com./a", "https://EXAMPLE.com/a"} {
		if _, key, domain, err := core.NormalizeURL(u); err != nil || domain != "example.com" || key != "example.com/a" {
			t.Errorf("NormalizeURL(%q) = %q %q %v", u, key, domain, err)
		}
	}
	for _, bad := range []string{"https://user@example.com/x", "https://user:pw@example.com/", "https://@example.com/", "https://./x", "ftp://example.com/x", "https:///x", "example.com", "", "https://example.com/" + strings.Repeat("a", 2000)} {
		if _, _, _, err := core.NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%.30q) accepted", bad)
		}
	}
}

func TestValidUsername(t *testing.T) {
	for in, ok := range map[string]bool{
		"a": false, "ab": true, strings.Repeat("a", 15): true, strings.Repeat("a", 16): false,
		"a_b-9": true, "a b": false, "a.b": false, "é1": false,
	} {
		if err := core.ValidUsername(in); (err == nil) != ok {
			t.Errorf("ValidUsername(%q) = %v", in, err)
		}
	}
	if core.UsernameLower("AbC") != "abc" {
		t.Error("UsernameLower")
	}
}
