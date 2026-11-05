package main

import (
	"maps"
	"strings"
	"testing"
)

func TestIDsFromSourcePath(t *testing.T) {
	cases := []struct {
		raw  string
		want clickIDs
	}{
		{"/landing?gclid=Cj0KCQ_abc-1.2~3&utm_source=ads", clickIDs{"gclid": "Cj0KCQ_abc-1.2~3"}},
		{"https://example.com/p?ttclid=TT123&li_fat_id=aaaa-bbbb#frag", clickIDs{"ttclid": "TT123", "li_fat_id": "aaaa-bbbb"}},
		{"/x?gbraid=G1&wbraid=W1&gclid=C1", clickIDs{"gbraid": "G1", "wbraid": "W1", "gclid": "C1"}},
		{"/x?gclid=bad%20value&ttclid=ok", clickIDs{"ttclid": "ok"}},
		{"/x?gclid=" + strings.Repeat("a", 257), clickIDs{}},
		{"/x?gclid=" + strings.Repeat("a", 256), clickIDs{"gclid": strings.Repeat("a", 256)}},
		{"/x?email=jane%40example.com&GCLID=up", clickIDs{}},
		{"/x#gclid=inFragment", nil},
		{"/x", nil},
		{"", nil},
		{"/x?%zz=1&gclid=ok", clickIDs{"gclid": "ok"}},
	}
	for _, c := range cases {
		got := idsFromSourcePath(c.raw)
		if len(got) != len(c.want) || (len(c.want) > 0 && !maps.Equal(got, c.want)) {
			t.Errorf("idsFromSourcePath(%q) = %v, want %v", c.raw, got, c.want)
		}
	}
}

func TestIDsFromSourcePathNeverReturnsOtherData(t *testing.T) {
	got := idsFromSourcePath("/thanks?email=jane%40example.com&phone=%2B491512345678&gclid=C1&name=Jane")
	if len(got) != 1 || got["gclid"] != "C1" {
		t.Fatalf("ids = %v", got)
	}
	for k, v := range got {
		if strings.Contains(k+v, "jane") || strings.Contains(k+v, "491512345678") {
			t.Fatalf("leaked %s=%s", k, v)
		}
	}
}

func TestCleanIDs(t *testing.T) {
	got := cleanIDs(map[string]any{
		"gclid":     "ok-1",
		"gbraid":    "bad value",
		"wbraid":    strings.Repeat("x", 257),
		"ttclid":    123.0,
		"li_fat_id": "li.1",
		"other":     "dropped",
		"email":     "jane@example.com",
	})
	if want := (clickIDs{"gclid": "ok-1", "li_fat_id": "li.1"}); !maps.Equal(got, want) {
		t.Fatalf("cleanIDs = %v, want %v", got, want)
	}
	if got := cleanIDs(nil); len(got) != 0 {
		t.Fatalf("cleanIDs(nil) = %v", got)
	}
}

func TestMergeIDsOverWinsAndInputsUntouched(t *testing.T) {
	base := clickIDs{"gclid": "old", "ttclid": "t"}
	over := clickIDs{"gclid": "new", "wbraid": "w"}
	got := mergeIDs(base, over)
	if want := (clickIDs{"gclid": "new", "ttclid": "t", "wbraid": "w"}); !maps.Equal(got, want) {
		t.Fatalf("mergeIDs = %v", got)
	}
	if base["gclid"] != "old" || len(over) != 2 {
		t.Fatal("inputs were modified")
	}
	if got := mergeIDs(nil, nil); got == nil || len(got) != 0 {
		t.Fatalf("mergeIDs(nil, nil) = %v", got)
	}
}

func TestCleanPath(t *testing.T) {
	cases := map[string]string{
		"/contact":                          "/contact",
		"/contact?email=jane@example.com#x": "/contact",
		"https://example.com/a/b?gclid=1":   "/a/b",
		"//evil.example/x":                  "",
		"contact":                           "",
		"/a b":                              "",
		"/a\\b":                             "",
		"":                                  "",
		"https://":                          "",
		"/" + strings.Repeat("a", maxPath):  "",
	}
	for in, want := range cases {
		if got := cleanPath(in); got != want {
			t.Errorf("cleanPath(%q) = %q, want %q", in, got, want)
		}
	}
}
