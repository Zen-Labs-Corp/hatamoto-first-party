package main

import (
	"strings"
	"testing"
)

func TestFiltersFromParams(t *testing.T) {
	long := strings.Repeat("a", 300)
	got := filtersFromParams(map[string]any{"category": " Music ", "venue": long, "city": 7, "free": true, "other": "x"})
	if got.Category != "Music" || len(got.Venue) != maxFilterBytes || got.City != "" || got.Price != "free" || got.Q != "" {
		t.Errorf("filters = %+v", got)
	}
	if f := filtersFromParams(map[string]any{"free": false}); !f.empty() {
		t.Errorf("free=false = %+v", f)
	}
	if !filtersFromParams(nil).empty() {
		t.Error("nil params not empty")
	}
}

func TestFiltersFromQuery(t *testing.T) {
	one := func(kv ...string) map[string][]string {
		m := map[string][]string{}
		for i := 0; i < len(kv); i += 2 {
			m[kv[i]] = []string{kv[i+1]}
		}
		return m
	}
	f, ok := filtersFromQuery(one("q", " jazz ", "category", "Music", "venue", "hall", "city", "Bonn", "price", "paid", "view", "week",
		"from", "2026-06-01", "to", "2026-07-01", "limit", "10", "slug", "gala"))
	if !ok || f != (filters{Q: "jazz", Category: "Music", Venue: "hall", City: "Bonn", Price: "paid"}) {
		t.Errorf("filters = %+v, %v", f, ok)
	}
	if f, ok := filtersFromQuery(nil); !ok || !f.empty() {
		t.Errorf("empty query = %+v, %v", f, ok)
	}
	if f, ok := filtersFromQuery(one("price", "any")); !ok || f.Price != "" {
		t.Errorf("price any = %+v, %v", f, ok)
	}
	bad := map[string]map[string][]string{
		"unknown key":    one("x", "1"),
		"repeated key":   {"q": {"a", "b"}},
		"no value":       {"q": {}},
		"oversized":      one("q", strings.Repeat("a", maxFilterBytes+1)),
		"bad price":      one("price", "cheap"),
		"bad view":       one("view", "carousel"),
		"unknown, valid": {"city": {"Bonn"}, "debug": {"1"}},
	}
	for name, q := range bad {
		if _, ok := filtersFromQuery(q); ok {
			t.Errorf("%s accepted", name)
		}
	}
}

func TestFiltersMatch(t *testing.T) {
	amt := func(v float64) *float64 { return &v }
	gala := Event{Name: "Summer Gala", Venue: "Blue Room", VenueCity: "Bonn", PriceAmount: amt(12)}
	cats := categoryRefs{Names: []string{"Music"}, Slugs: []string{"live-music"}}
	tests := []struct {
		name string
		f    filters
		e    Event
		want bool
	}{
		{"empty", filters{}, gala, true},
		{"q name", filters{Q: "gala"}, gala, true},
		{"q venue", filters{Q: "BLUE"}, gala, true},
		{"q miss", filters{Q: "fair"}, gala, false},
		{"category name", filters{Category: "music"}, gala, true},
		{"category slug", filters{Category: "Live-Music"}, gala, true},
		{"category miss", filters{Category: "art"}, gala, false},
		{"venue", filters{Venue: "blue room"}, gala, true},
		{"venue miss", filters{Venue: "red room"}, gala, false},
		{"city", filters{City: "BONN"}, gala, true},
		{"city miss", filters{City: "Köln"}, gala, false},
		{"paid", filters{Price: "paid"}, gala, true},
		{"free vs paid", filters{Price: "free"}, gala, false},
		{"free amount 0", filters{Price: "free"}, Event{PriceAmount: amt(0)}, true},
		{"free text", filters{Price: "free"}, Event{Price: " Free "}, true},
		{"free text paid", filters{Price: "paid"}, Event{Price: "free"}, false},
		{"paid text", filters{Price: "paid"}, Event{Price: "5 EUR"}, true},
		{"no price data", filters{Price: "free"}, Event{}, false},
		{"no price data paid", filters{Price: "paid"}, Event{}, false},
	}
	for _, tc := range tests {
		if got := tc.f.match(tc.e, cats); got != tc.want {
			t.Errorf("%s: match = %v, want %v", tc.name, got, tc.want)
		}
	}
}
