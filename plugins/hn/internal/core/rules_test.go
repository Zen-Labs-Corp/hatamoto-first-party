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

func TestNormalizeURLPort(t *testing.T) {
	for _, bad := range []string{"http://a.example:99999/", "http://a.example:65536/", "http://a.example:0/", "http://a.example:abc/"} {
		if _, _, _, err := core.NormalizeURL(bad); err == nil {
			t.Errorf("NormalizeURL(%q) accepted", bad)
		}
	}
	for _, good := range []string{"http://a.example:65535/", "http://a.example:8080/x", "http://a.example:/x"} {
		if _, _, _, err := core.NormalizeURL(good); err != nil {
			t.Errorf("NormalizeURL(%q) = %v", good, err)
		}
	}
	if canon, _, _, _ := core.NormalizeURL("http://a.example:8080/x"); canon != "http://a.example:8080/x" {
		t.Errorf("canon %q", canon)
	}
}

func TestNormalizeURLIDN(t *testing.T) {
	_, k1, d1, err := core.NormalizeURL("http://münchen-qa.example/a")
	if err != nil || d1 != "xn--mnchen-qa-q9a.example" {
		t.Fatalf("got %q %v", d1, err)
	}
	canon, k2, _, _ := core.NormalizeURL("http://xn--mnchen-qa-q9a.example/a")
	if k1 != k2 {
		t.Errorf("keys %q %q", k1, k2)
	}
	if c, k3, _, _ := core.NormalizeURL("http://MÜNCHEN-qa.example:8080/a"); k3 != "xn--mnchen-qa-q9a.example/a" || c != "http://xn--mnchen-qa-q9a.example:8080/a" {
		t.Errorf("canon %q key %q", c, k3)
	}
	if c, _, _, _ := core.NormalizeURL("http://münchen-qa.example/a"); c != canon {
		t.Errorf("canon %q, want %q", c, canon)
	}
	if _, _, d, err := core.NormalizeURL("http://a_b.example/"); err != nil || d != "a_b.example" {
		t.Errorf("underscore host: %q %v", d, err)
	}
	if _, _, _, err := core.NormalizeURL("http://a\ufffdb.example/"); err == nil {
		t.Error("invalid IDN host accepted")
	}
}

func TestASCIIHost(t *testing.T) {
	for in, want := range map[string]string{
		"Example.COM": "example.com", "münchen-qa.example": "xn--mnchen-qa-q9a.example",
		"MÜNCHEN-qa.example": "xn--mnchen-qa-q9a.example", "a_b.example": "a_b.example",
	} {
		if got, ok := core.ASCIIHost(in); !ok || got != want {
			t.Errorf("ASCIIHost(%q) = %q %v, want %q", in, got, ok, want)
		}
	}
	if _, ok := core.ASCIIHost("a\ufffdb.example"); ok {
		t.Error("U+FFFD host converted")
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
