package main

import (
	"errors"
	"strings"
	"testing"
)

func TestIsUUID(t *testing.T) {
	for in, want := range map[string]bool{
		"123e4567-e89b-12d3-a456-426614174000":  true,
		"123E4567-E89B-12D3-A456-426614174000":  true,
		"":                                      false,
		"123e4567e89b12d3a456426614174000":      false,
		"123e4567-e89b-12d3-a456-42661417400g":  false,
		"123e4567-e89b-12d3-a456-4266141740001": false,
		"123e4567_e89b-12d3-a456-426614174000":  false,
	} {
		if got := isUUID(in); got != want {
			t.Errorf("isUUID(%q) = %v", in, got)
		}
	}
}

func TestCleanPath(t *testing.T) {
	for _, tc := range []struct {
		in string
		ok bool
	}{
		{"/", true},
		{"/blog/a?x=1", true},
		{"/a/b#frag", true},
		{"/" + strings.Repeat("a", maxPath-1), true},
		{"/" + strings.Repeat("a", maxPath), false},
		{"", false},
		{"evil", false},
		{"//evil.com", false},
		{"/\\evil", false},
		{"/a\\b", false},
		{"http://x", false},
		{"javascript:x", false},
		{"/a b", false},
		{"/a\nb", false},
		{"/a\tb", false},
		{"/\x7f", false},
		{"/é", false},
		{"/%zz", false},
	} {
		got, ok := cleanPath(tc.in)
		if ok != tc.ok || (ok && got != tc.in) || (!ok && got != "") {
			t.Errorf("cleanPath(%q) = %q, %v", tc.in, got, ok)
		}
	}
}

func TestNewVoterFormatAndUniqueness(t *testing.T) {
	seen := map[string]bool{}
	for range 50 {
		v, err := newVoter()
		if err != nil || !validVoter(v) {
			t.Fatalf("newVoter() = %q, %v", v, err)
		}
		if seen[v] {
			t.Fatalf("duplicate voter %q", v)
		}
		seen[v] = true
	}
}

func TestValidVoter(t *testing.T) {
	for in, want := range map[string]bool{
		"0123456789abcdef0123456789abcdef":  true,
		"0123456789ABCDEF0123456789ABCDEF":  false,
		"0123456789abcdef0123456789abcde":   false,
		"0123456789abcdef0123456789abcdef0": false,
		"0123456789abcdef0123456789abcdeg":  false,
		"":                                  false,
	} {
		if got := validVoter(in); got != want {
			t.Errorf("validVoter(%q) = %v", in, got)
		}
	}
}

func TestLoadConfig(t *testing.T) {
	for _, tc := range []struct {
		name     string
		settings string
		err      error
		schemas  []string
		allow    bool
	}{
		{"host error", "", errors.New("boom"), []string{"post"}, false},
		{"empty object", `{}`, nil, []string{"post"}, false},
		{"both set", `{"schemas":["page"," post ",""],"allow_change":true}`, nil, []string{"page", "post"}, true},
		{"bad schemas keeps allow_change", `{"schemas":"post","allow_change":true}`, nil, []string{"post"}, true},
		{"bad allow_change keeps schemas", `{"schemas":["page"],"allow_change":"yes"}`, nil, []string{"page"}, false},
		{"empty list means none", `{"schemas":[]}`, nil, nil, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			(&fakeHost{settings: tc.settings, settingsErr: tc.err}).install(t)
			c := loadConfig()
			if strings.Join(c.Schemas, ",") != strings.Join(tc.schemas, ",") || c.AllowChange != tc.allow {
				t.Fatalf("config = %+v", c)
			}
		})
	}
}

func TestConfigAllows(t *testing.T) {
	c := config{Schemas: []string{"post", "page"}}
	for in, want := range map[string]bool{"post": true, "page": true, "event": false, "": false} {
		if got := c.allows(in); got != want {
			t.Errorf("allows(%q) = %v", in, got)
		}
	}
}
