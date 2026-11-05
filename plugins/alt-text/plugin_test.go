package main

import (
	"encoding/json"
	"testing"
)

func TestParseParams(t *testing.T) {
	cases := []struct {
		in   string
		want params
	}{
		{``, params{50, true}},
		{`{}`, params{50, true}},
		{`{"limit":0}`, params{50, true}},
		{`{"limit":-3}`, params{50, true}},
		{`{"limit":7}`, params{7, true}},
		{`{"limit":7.0,"only_missing":false}`, params{7, false}},
		{`{"limit":1000,"only_missing":true}`, params{200, true}},
		{`{"limit":0.5}`, params{50, true}},
		{`not json`, params{50, true}},
	}
	for _, c := range cases {
		if got := parseParams(json.RawMessage(c.in)); got != c.want {
			t.Errorf("parseParams(%q) = %+v, want %+v", c.in, got, c.want)
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
