package main

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func ask(t *testing.T, source string, limit int) sdk.BuilderDataResult {
	t.Helper()
	in, _ := json.Marshal(sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "q1", Source: source, Limit: limit}}})
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var req sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &req); err != nil {
		t.Fatal(err)
	}
	if len(req.Results) != 1 || req.Results[0].ID != "q1" {
		t.Fatalf("results %+v", req.Results)
	}
	return req.Results[0]
}

func seeded(t *testing.T) *fakeHost {
	f := newFake(t)
	g1 := rv("google", "g1", 5, 30)
	g1.AuthorURL, g1.PhotoURL, g1.Link = "https://example.test/a", "https://example.test/p.jpg", "https://example.test/r"
	f.seed(t, sourceGoogle, 4.8, 132, g1, rv("google", "g2", 3, 31), rv("google", "g3", 4, 10))
	f.seed(t, sourceTrustpilot, 4.6, 210, rv("trustpilot", "t1", 5, 21))
	return f
}

func TestBuilderMapsItems(t *testing.T) {
	seeded(t)
	res := ask(t, "ext.reviews.latest", 0)
	if res.Error != "" || len(res.Items) != 3 {
		t.Fatalf("%+v", res)
	}
	it := res.Items[0]
	if it.Title != "A g1" || it.Author != "A g1" || it.Excerpt != "text g1" || it.URL != "https://example.test/r" ||
		it.AuthorURL != "https://example.test/a" || it.Image != "https://example.test/p.jpg" ||
		it.Category != "Google" || it.PublishedAt != "2026-10-30T09:00:00Z" {
		t.Fatalf("item %+v", it)
	}
	if it.Attrs["rating"] != "5" || it.Attrs["date"] != "2026-10-30" || it.Attrs["source"] != "google" || it.Attrs["url"] != "https://example.test/r" {
		t.Fatalf("attrs %+v", it.Attrs)
	}
	// min_rating 4 drops g2 (3 stars); newest first; no review link -> profile link.
	if res.Items[1].Attrs["source"] != "trustpilot" || res.Items[1].URL != "https://example.test/trustpilot" || res.Items[2].Title != "A g3" {
		t.Fatalf("order %+v", res.Items)
	}
}

func TestBuilderCaps(t *testing.T) {
	f := newFake(t)
	var rs []review
	for i := 0; i < 60; i++ {
		rs = append(rs, rv("google", "x"+itoa(i), 5, 1+i%28))
	}
	f.seed(t, sourceGoogle, 5, 60, rs...)
	if n := len(ask(t, "ext.reviews.latest", 0).Items); n != 6 {
		t.Fatalf("default max_items: %d", n)
	}
	if n := len(ask(t, "ext.reviews.latest", 2).Items); n != 2 {
		t.Fatalf("limit: %d", n)
	}
	f.set("max_items", 100)
	invalidateCache()
	if n := len(ask(t, "ext.reviews.latest", 0).Items); n != 48 {
		t.Fatalf("48 cap: %d", n)
	}
	f.set("max_items", 3)
	invalidateCache()
	if n := len(ask(t, "ext.reviews.latest", 10).Items); n != 3 {
		t.Fatalf("max_items: %d", n)
	}
}

func TestBuilderClipsMultibyteText(t *testing.T) {
	f := newFake(t)
	r := rv("google", "g1", 5, 1)
	r.Body = strings.Repeat("é", 3000)
	r.Author = strings.Repeat("日", 2000)
	f.seed(t, sourceGoogle, 5, 1, r)
	it := ask(t, "ext.reviews.latest", 0).Items[0]
	for _, s := range []string{it.Excerpt, it.Title, it.Author} {
		if len(s) >= 2048 || !utf8.ValidString(s) || s == "" {
			t.Fatalf("bad string len=%d valid=%v", len(s), utf8.ValidString(s))
		}
	}
}

func TestBuilderFailsOpen(t *testing.T) {
	f := seeded(t)
	if res := ask(t, "ext.reviews.nope", 0); res.Error != "" || len(res.Items) != 0 {
		t.Fatalf("unknown source: %+v", res)
	}
	invalidateCache()
	f.dbDown = true
	if res := ask(t, "ext.reviews.latest", 0); res.Error != "" || len(res.Items) != 0 {
		t.Fatalf("db down: %+v", res)
	}
	f.dbDown = false
	f.settingsErr = errTest
	invalidateCache()
	if res := ask(t, "ext.reviews.latest", 0); res.Error != "" || len(res.Items) != 0 {
		t.Fatalf("settings down: %+v", res)
	}
}

func TestBuilderEmptyDbAndNoKeys(t *testing.T) {
	newFake(t)
	if res := ask(t, "ext.reviews.latest", 0); res.Error != "" || len(res.Items) != 0 {
		t.Fatalf("%+v", res)
	}
}
