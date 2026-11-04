package main

import (
	"strings"
	"testing"
	"time"
)

// bothAPIs answers both sources with one review each.
func bothAPIs(f *fakeHost) {
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		switch {
		case strings.HasPrefix(u, "https://places.googleapis.com/"):
			return 200, googleFixture, nil
		case strings.Contains(u, "/reviews?"):
			return 200, tpReviewsFixture, nil
		}
		return 200, tpUnitFixture, nil
	}
}

func hosts(f *fakeHost) (g, tp int) {
	for _, r := range f.requests {
		if strings.Contains(r.URL, "googleapis") {
			g++
		} else {
			tp++
		}
	}
	return
}

func TestRefreshFetchesBothWhenNeverAttempted(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	if err := refreshAll(false); err != nil {
		t.Fatal(err)
	}
	g, tp := hosts(f)
	rs, _ := loadReviews()
	src, _ := loadSources()
	if g != 1 || tp != 2 || len(rs) != 5 || src[sourceGoogle].Count != 132 || src[sourceTrustpilot].Count != 210 {
		t.Fatalf("g=%d tp=%d reviews=%d sources=%+v", g, tp, len(rs), src)
	}
}

func TestRefreshHonoursTheDailyGate(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	_ = refreshAll(false)
	f.requests = nil

	f.clock = f.clock.Add(22 * time.Hour)
	_ = refreshAll(false)
	if g, tp := hosts(f); g != 0 || tp != 0 {
		t.Fatalf("22h: g=%d tp=%d", g, tp)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	_ = refreshAll(false)
	if g, tp := hosts(f); g != 1 || tp != 2 {
		t.Fatalf("24h: g=%d tp=%d", g, tp)
	}
	f.requests = nil
	_ = refreshAll(true)
	if g, tp := hosts(f); g != 1 || tp != 2 {
		t.Fatalf("force: g=%d tp=%d", g, tp)
	}
}

func TestOneSourceFailingKeepsTheOther(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	_ = refreshAll(false)
	good := f.fetch
	f.fetch = func(u string, h map[string]string) (int, string, error) {
		if strings.Contains(u, "googleapis") {
			return 403, `{"error":{"message":"key G-SECRET rejected"}}`, nil
		}
		return good(u, h)
	}
	f.clock = f.clock.Add(24 * time.Hour)
	if err := refreshAll(false); err != nil {
		t.Fatal(err)
	}
	src, _ := loadSources()
	rs, _ := loadReviews()
	if !strings.Contains(src[sourceGoogle].LastError, "403") || strings.Contains(src[sourceGoogle].LastError, "G-SECRET") {
		t.Fatalf("stored error %q", src[sourceGoogle].LastError)
	}
	if src[sourceGoogle].Count != 132 || len(rs) != 5 || src[sourceTrustpilot].LastError != "" {
		t.Fatalf("data changed: %+v reviews=%d", src, len(rs))
	}
	if strings.Contains(f.logged(), "G-SECRET") || !strings.Contains(f.logged(), "google refresh failed") {
		t.Fatalf("log %q", f.logged())
	}
}

func TestRefreshWithoutKeysIsQuiet(t *testing.T) {
	f := newFake(t)
	if err := refreshAll(true); err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 0 {
		t.Fatalf("requests %+v", f.requests)
	}
	f.set("google_api_key", "only-a-key")
	if err := refreshAll(true); err != nil || len(f.requests) != 0 {
		t.Fatalf("half configured: %v %+v", err, f.requests)
	}
}

func TestRetention(t *testing.T) {
	f := newFake(t)
	f.seed(t, sourceGoogle, 4.8, 1, rv("google", "g1", 5, 1))
	f.clock = f.clock.Add(29 * 24 * time.Hour)
	if err := refreshAll(false); err != nil {
		t.Fatal(err)
	}
	if rs, _ := loadReviews(); len(rs) != 1 {
		t.Fatalf("29 days: %d reviews", len(rs))
	}
	f.clock = f.clock.Add(2 * 24 * time.Hour)
	if err := refreshAll(false); err != nil {
		t.Fatal(err)
	}
	rs, _ := loadReviews()
	src, _ := loadSources()
	if len(rs) != 0 || len(src) != 0 {
		t.Fatalf("31 days (unconfigured): %d reviews, %+v", len(rs), src)
	}
}

func TestRetentionAlsoWhenRefreshKeepsFailing(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	f.seed(t, sourceGoogle, 4.8, 1, rv("google", "g1", 5, 1))
	f.fetch = func(string, map[string]string) (int, string, error) { return 500, `{}`, nil }
	f.clock = f.clock.Add(31 * 24 * time.Hour)
	_ = refreshAll(false)
	if rs, _ := loadReviews(); len(rs) != 0 {
		t.Fatalf("stale data kept: %d", len(rs))
	}
}

func TestRefreshStopsWhenTimeRunsOut(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	inner := f.fetch
	f.fetch = func(u string, h map[string]string) (int, string, error) {
		f.clock = f.clock.Add(6 * time.Second) // Google is slow
		return inner(u, h)
	}
	_ = refreshAll(false)
	if g, tp := hosts(f); g != 1 || tp != 0 {
		t.Fatalf("g=%d tp=%d", g, tp)
	}
	src, _ := loadSources()
	if _, ok := src[sourceTrustpilot]; ok {
		t.Fatal("a skipped source must not be marked as attempted")
	}
}

func TestRefreshFailsOpenWhenDbIsDown(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	f.dbDown = true
	_ = refreshAll(true) // must not panic
	if len(f.requests) != 0 {
		t.Fatalf("no db state, no fetch: %+v", f.requests)
	}
}

func TestFailedSourceIsRetriedAfterAnHour(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	f.fetch = func(string, map[string]string) (int, string, error) {
		return 403, `{"error":{"message":"bad key"}}`, nil
	}
	_ = refreshAll(false)
	f.requests = nil

	f.clock = f.clock.Add(30 * time.Minute)
	_ = refreshAll(false)
	if g, tp := hosts(f); g != 0 || tp != 0 {
		t.Fatalf("30m: g=%d tp=%d", g, tp)
	}
	f.clock = f.clock.Add(31 * time.Minute)
	_ = refreshAll(false)
	if g, tp := hosts(f); g != 1 || tp != 1 {
		t.Fatalf("61m: g=%d tp=%d", g, tp)
	}
}

func TestRunBudgetLeavesRoomForWrites(t *testing.T) {
	if runBudget > 8*time.Second {
		t.Fatalf("runBudget %v leaves under 2s of the 10s hook for the writes", runBudget)
	}
}
