package main

import (
	"errors"
	"testing"
	"time"

	"hatamoto.dev/sdk/guest"
)

var errTest = errors.New("down")

func fn(t *testing.T, name, args string) string {
	t.Helper()
	out, err := guest.Handle(guest.FuncExport(name), []byte(args))
	if err != nil {
		t.Fatalf("%s returned an error: %v", name, err)
	}
	return string(out)
}

func TestSummary(t *testing.T) {
	f := newFake(t)
	if got := fn(t, "summary", `[]`); got != "" {
		t.Fatalf("nothing cached: %q", got)
	}
	f.seed(t, sourceGoogle, 4.8, 132)
	invalidateCache()
	if got := fn(t, "summary", `[]`); got != "4.8 from 132 Google reviews" {
		t.Fatalf("google only: %q", got)
	}
	f.seed(t, sourceTrustpilot, 4.6, 210)
	invalidateCache()
	if got := fn(t, "summary", `[]`); got != "4.8 from 132 Google reviews, 4.6 from 210 on Trustpilot" {
		t.Fatalf("both: %q", got)
	}
	if got := fn(t, "summary", `["trustpilot"]`); got != "4.6 from 210 Trustpilot reviews" {
		t.Fatalf("trustpilot arg: %q", got)
	}
	if got := fn(t, "summary", `["google"]`); got != "4.8 from 132 Google reviews" {
		t.Fatalf("google arg: %q", got)
	}
	if got := fn(t, "summary", `["bing"]`); got == "" {
		t.Fatal("unknown source arg means all")
	}
	f.seed(t, sourceGoogle, 5, 1)
	invalidateCache()
	if got := fn(t, "summary", `["google"]`); got != "5 from 1 Google review" {
		t.Fatalf("singular: %q", got)
	}
}

func TestCountFieldProfile(t *testing.T) {
	seeded(t)
	if got := fn(t, "count", `[]`); got != "3" {
		t.Fatalf("count %q", got)
	}
	if got := fn(t, "count", `["google"]`); got != "2" {
		t.Fatalf("count google %q", got)
	}
	if got := fn(t, "count", `["trustpilot"]`); got != "1" {
		t.Fatalf("count trustpilot %q", got)
	}
	want := map[string]string{"author": "A g1", "text": "text g1", "date": "2026-10-30", "rating": "5", "stars": "★★★★★",
		"source": "google", "source_label": "Google", "url": "https://example.test/r", "author_url": "https://example.test/a", "photo": "https://example.test/p.jpg"}
	for k, v := range want {
		if got := fn(t, "field", `[0,"`+k+`"]`); got != v {
			t.Fatalf("field %s = %q, want %q", k, got, v)
		}
	}
	if got := fn(t, "field", `[2,"stars"]`); got != "★★★★☆" {
		t.Fatalf("stars %q", got)
	}
	for _, bad := range []string{`[3,"author"]`, `[-1,"author"]`, `[0,"nope"]`, `[0.5,"author"]`, `["x","author"]`, `[0]`, `[]`} {
		if got := fn(t, "field", bad); got != "" {
			t.Fatalf("field %s = %q", bad, got)
		}
	}
	if got := fn(t, "profile", `["google"]`); got != "https://example.test/google" {
		t.Fatalf("profile %q", got)
	}
	if got := fn(t, "profile", `["trustpilot"]`); got != "https://example.test/trustpilot" {
		t.Fatalf("profile %q", got)
	}
	if got := fn(t, "profile", `[]`); got != "" {
		t.Fatalf("profile without arg %q", got)
	}
}

func TestFuncsWithoutKeysOrDbAreEmpty(t *testing.T) {
	f := newFake(t)
	f.dbDown = true
	for _, c := range []struct{ name, args, want string }{
		{"summary", `[]`, ""}, {"count", `[]`, ""}, {"field", `[0,"author"]`, ""}, {"profile", `["google"]`, ""},
	} {
		if got := fn(t, c.name, c.args); got != c.want {
			t.Fatalf("%s = %q", c.name, got)
		}
	}
}

func TestCacheServesRepeatCallsAndInvalidates(t *testing.T) {
	f := seeded(t)
	fn(t, "count", `[]`)
	q := f.queries
	fn(t, "field", `[0,"author"]`)
	fn(t, "summary", `[]`)
	if f.queries != q {
		t.Fatalf("cache missed: %d -> %d queries", q, f.queries)
	}
	f.clock = f.clock.Add(2 * time.Minute)
	fn(t, "count", `[]`)
	if f.queries == q {
		t.Fatal("expired cache must reload")
	}
	q = f.queries
	invalidateCache()
	fn(t, "count", `[]`)
	if f.queries == q {
		t.Fatal("invalidate must reload")
	}
}

func TestRefreshInvalidatesTheCache(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	if got := fn(t, "count", `[]`); got != "" {
		t.Fatal(got)
	}
	_ = refreshAll(true)
	if got := fn(t, "count", `[]`); got == "" {
		t.Fatal("refresh must drop the empty snapshot")
	}
}

func TestUnconfiguredSourceIsHiddenEverywhere(t *testing.T) {
	f := newFake(t)
	f.seed(t, sourceGoogle, 4.8, 132, rv("google", "g1", 5, 30))
	f.seed(t, sourceTrustpilot, 4.6, 210, rv("trustpilot", "t1", 5, 21))
	invalidateCache()
	if got := fn(t, "count", `[]`); got != "2" {
		t.Fatalf("both set up: %q", got)
	}
	f.set("google_api_key", "")
	invalidateCache()
	if got := fn(t, "count", `[]`); got != "1" {
		t.Fatalf("count after clearing the Google key: %q", got)
	}
	if got := fn(t, "summary", `[]`); got != "4.6 from 210 Trustpilot reviews" {
		t.Fatalf("summary after clearing the Google key: %q", got)
	}
	if got := fn(t, "profile", `["google"]`); got != "" {
		t.Fatalf("profile: %q", got)
	}
	if n := len(ask(t, "ext.reviews.latest", 10).Items); n != 1 {
		t.Fatalf("builder shows %d items, want 1", n)
	}
	for _, r := range buildOverview().Reviews {
		if r.Source == sourceGoogle {
			t.Fatalf("overview lists a Google review: %+v", r)
		}
	}
	f.set("trustpilot_business_unit_id", "")
	invalidateCache()
	if got := fn(t, "count", `[]`); got != "" || fn(t, "summary", `[]`) != "" {
		t.Fatalf("nothing is set up but %q shown", got)
	}
}
