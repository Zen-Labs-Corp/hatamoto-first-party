package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// setFeeds configures feeds named in order; each URL is https://example.com/<name>.xml.
func setFeeds(f *fakeHost, names ...string) {
	var feeds []map[string]any
	for _, n := range names {
		feeds = append(feeds, map[string]any{"name": n, "url": "https://example.com/" + n + ".xml", "max_items": 50})
	}
	f.settings["feeds"] = feeds
}

func rss(n int) string {
	var sb strings.Builder
	sb.WriteString(`<rss version="2.0"><channel><title>T</title><link>https://example.com/</link>`)
	for i := 0; i < n; i++ {
		fmt.Fprintf(&sb, `<item><title>Item %d</title><link>https://example.com/i/%d</link><guid>g%d</guid><pubDate>Tue, 03 Nov 2026 %02d:00:00 GMT</pubDate></item>`, i, i, i, 23-i%24)
	}
	sb.WriteString(`</channel></rss>`)
	return sb.String()
}

func statusOf(results []feedResult) string {
	var parts []string
	for _, r := range results {
		parts = append(parts, r.Name+"="+r.Status)
	}
	return strings.Join(parts, ",")
}

func TestRefreshStoresItemsAndState(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	serve(f, fetchRes{Status: 200, Body: rss(3), Headers: map[string]string{"etag": `"e1"`, "last-modified": "Tue, 03 Nov 2026 09:30:00 GMT"}})
	if got := statusOf(refresh(false)); got != "alpha=ok" {
		t.Fatalf("results %s", got)
	}
	row := f.feeds[f.keyOf("alpha")]
	if row.Status != "ok" || row.ETag != `"e1"` || row.ItemCount != 3 || row.SiteURL != "https://example.com/" || !row.LastAttempt.Equal(f.clock) {
		t.Fatalf("row %+v", row)
	}
	if len(f.itemsOf(row.Key)) != 3 {
		t.Fatalf("items %d", len(f.itemsOf(row.Key)))
	}
}

func (f *fakeHost) keyOf(name string) string {
	for k, r := range f.feeds {
		if r.Name == name {
			return k
		}
	}
	f.t.Fatalf("no feed %q", name)
	return ""
}

func TestRefreshOnlyFetchesDueFeedsThenUsesConditionalGet(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	serve(f, fetchRes{Status: 200, Body: rss(2), Headers: map[string]string{"etag": `"e1"`}})
	refresh(false)
	f.clock = f.clock.Add(29 * time.Minute)
	if res := refresh(false); len(res) != 0 || len(f.requests) != 1 {
		t.Fatalf("not due yet: results %v, requests %d", res, len(f.requests))
	}
	f.clock = f.clock.Add(2 * time.Minute)
	serve(f, fetchRes{Status: 304})
	if got := statusOf(refresh(false)); got != "alpha=not modified" {
		t.Fatalf("results %s", got)
	}
	if h := f.requests[1].Headers; h["If-None-Match"] != `"e1"` {
		t.Fatalf("headers %v", h)
	}
	row := f.feeds[f.keyOf("alpha")]
	if row.ETag != `"e1"` || row.ItemCount != 2 || row.Status != "not modified" || len(f.itemsOf(row.Key)) != 2 {
		t.Fatalf("304 must keep items and validators: %+v", row)
	}
}

func TestRefreshOneFailingFeedDoesNotStopOthers(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "bad500", "badxml", "huge", "slow", "good")
	f.fetch = func(u string, _ map[string]string) (fetchRes, error) {
		switch {
		case strings.Contains(u, "bad500"):
			return fetchRes{Status: 500, Body: "SECRET-BODY"}, nil
		case strings.Contains(u, "badxml"):
			return fetchRes{Status: 200, Body: "<rss>SECRET-BODY"}, nil
		case strings.Contains(u, "huge"):
			return fetchRes{Status: 200, Body: "x", Truncated: true}, nil
		case strings.Contains(u, "slow"):
			return fetchRes{}, &sdk.HostError{Code: sdk.ErrTimeout, Message: "GET " + u + " SECRET-BODY"}
		}
		return fetchRes{Status: 200, Body: rss(2)}, nil
	}
	got := statusOf(refresh(false))
	want := "bad500=HTTP 500,badxml=invalid feed,good=ok,huge=feed too large,slow=timeout"
	// The run is in key order for never-attempted feeds, so compare as a set.
	if !sameSet(got, want) {
		t.Fatalf("results %s", got)
	}
	if len(f.itemsOf(f.keyOf("good"))) != 2 {
		t.Fatal("the good feed was not stored")
	}
	for _, r := range f.feeds {
		if strings.Contains(r.Status, "SECRET") || strings.Contains(r.Status, "example.com") {
			t.Fatalf("status leaks: %q", r.Status)
		}
	}
	if strings.Contains(f.logged(), "SECRET") || strings.Contains(f.logged(), "example.com") {
		t.Fatalf("log leaks:\n%s", f.logged())
	}
	if f.feeds[f.keyOf("bad500")].LastAttempt.IsZero() {
		t.Fatal("a failed attempt must still be recorded")
	}
}

func sameSet(a, b string) bool {
	x, y := strings.Split(a, ","), strings.Split(b, ",")
	if len(x) != len(y) {
		return false
	}
	seen := map[string]int{}
	for _, s := range x {
		seen[s]++
	}
	for _, s := range y {
		seen[s]--
	}
	for _, n := range seen {
		if n != 0 {
			return false
		}
	}
	return true
}

func TestRefreshFailureKeepsOldValidatorsAndRetriesAfterInterval(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	serve(f, fetchRes{Status: 200, Body: rss(2), Headers: map[string]string{"etag": `"e1"`}})
	refresh(false)
	f.clock = f.clock.Add(31 * time.Minute)
	serve(f, fetchRes{Status: 503})
	refresh(false)
	row := f.feeds[f.keyOf("alpha")]
	if row.Status != "HTTP 503" || row.ETag != `"e1"` || row.ItemCount != 2 || len(f.itemsOf(row.Key)) != 2 {
		t.Fatalf("row %+v", row)
	}
	f.clock = f.clock.Add(5 * time.Minute)
	if res := refresh(false); len(res) != 0 {
		t.Fatalf("a failed feed waits the normal interval: %v", res)
	}
}

func TestRefreshBudgetStopsStartingAndMarksRestDue(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "a", "b", "c", "d")
	f.fetch = func(string, map[string]string) (fetchRes, error) {
		f.clock = f.clock.Add(2500 * time.Millisecond) // every feed takes 2.5 s
		return fetchRes{Status: 200, Body: rss(1)}, nil
	}
	res := refresh(false)
	// Starts at 0 s, 2.5 s and 5 s; at 7.5 s nothing more is started.
	if len(f.requests) != 3 {
		t.Fatalf("requests %d, results %v", len(f.requests), res)
	}
	deferred := 0
	for _, r := range res {
		if r.Status == statusDeferred {
			deferred++
		}
	}
	if len(res) != 4 || deferred != 1 {
		t.Fatalf("results %v", res)
	}
	var waiting feedRow
	for _, r := range f.feeds {
		if r.LastAttempt.IsZero() {
			waiting = r
		}
	}
	if waiting.Key == "" {
		t.Fatal("the deferred feed must be marked due")
	}
	// The next run takes it first, even though the others are due soon too.
	f.fetch = func(u string, _ map[string]string) (fetchRes, error) { return fetchRes{Status: 200, Body: rss(1)}, nil }
	f.requests = nil
	next := refresh(false)
	if len(next) != 1 || next[0].Name != waiting.Name {
		t.Fatalf("next run %v, want only %s", next, waiting.Name)
	}
}

func TestRefreshRequestTimeoutBoundedByBudget(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "a", "b")
	f.fetch = func(string, map[string]string) (fetchRes, error) {
		f.clock = f.clock.Add(6 * time.Second)
		return fetchRes{Status: 200, Body: rss(1)}, nil
	}
	refresh(false)
	if len(f.requests) != 2 || f.requests[0].TimeoutMS != 8000 || f.requests[1].TimeoutMS != 3000 {
		t.Fatalf("requests %+v", f.requests)
	}
}

func TestRefreshForceIgnoresDueTime(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "a", "b")
	serve(f, fetchRes{Status: 200, Body: rss(1)})
	refresh(false)
	f.requests = nil
	if res := refresh(false); len(res) != 0 {
		t.Fatalf("not due: %v", res)
	}
	if res := refresh(true); len(res) != 2 || len(f.requests) != 2 {
		t.Fatalf("force: results %v, requests %d", res, len(f.requests))
	}
}

func TestRefreshAppliesMaxItemsAndRemovesDroppedFeeds(t *testing.T) {
	f := newFake(t)
	f.settings["feeds"] = []map[string]any{
		{"name": "a", "url": "https://example.com/a.xml", "max_items": 2},
		{"name": "b", "url": "https://example.com/b.xml"},
	}
	serve(f, fetchRes{Status: 200, Body: rss(6)})
	refresh(false)
	if got := f.feeds[f.keyOf("a")].ItemCount; got != 2 {
		t.Fatalf("a item_count %d", got)
	}
	if got := f.feeds[f.keyOf("b")].ItemCount; got != 6 {
		t.Fatalf("b item_count %d (default max is 50)", got)
	}
	f.settings["feeds"] = []map[string]any{{"name": "a", "url": "https://example.com/a.xml", "max_items": 2}}
	refresh(false)
	if len(f.feeds) != 1 || len(f.items) != 2 {
		t.Fatalf("feeds %d, items %d", len(f.feeds), len(f.items))
	}
}

func TestRefreshFailsOpen(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "a")
	serve(f, fetchRes{Status: 200, Body: rss(1)})
	f.settingsErr = fmt.Errorf("settings down")
	if res := refresh(false); res != nil {
		t.Fatalf("results %v", res)
	}
	f.settingsErr = nil
	f.dbDown = true
	if res := refresh(true); res != nil {
		t.Fatalf("results %v", res)
	}
	if len(f.requests) != 0 || !strings.Contains(f.logged(), "feed-reader:") {
		t.Fatalf("requests %d, logs %q", len(f.requests), f.logged())
	}
}

func TestCronHandlerNeverFails(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "a")
	serve(f, fetchRes{Status: 200, Body: rss(2)})
	in, _ := json.Marshal(sdk.CronEvent{Name: "fetch"})
	if _, err := guest.Handle(guest.CronExport("fetch"), in); err != nil {
		t.Fatal(err)
	}
	if len(f.itemsOf(f.keyOf("a"))) != 2 {
		t.Fatal("cron did not fetch")
	}
	f.dbDown = true
	f.settingsErr = fmt.Errorf("down")
	if _, err := guest.Handle(guest.CronExport("fetch"), in); err != nil {
		t.Fatalf("cron must fail open: %v", err)
	}
}

func TestRefreshClaimsFeedBeforeFetching(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "poison")
	var claimed time.Time
	f.fetch = func(string, map[string]string) (fetchRes, error) {
		claimed = f.feeds[f.keyOf("poison")].LastAttempt // the run is killed here
		return fetchRes{Status: 200, Body: rss(1)}, nil
	}
	refresh(false)
	if !claimed.Equal(f.clock) {
		t.Fatalf("attempt time during the fetch %v, want %v", claimed, f.clock)
	}
}

func TestRefreshLateDownloadIsNotParsedAndStaysDue(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "slow")
	f.fetch = func(string, map[string]string) (fetchRes, error) {
		f.clock = f.clock.Add(8 * time.Second)
		return fetchRes{Status: 200, Body: rss(2), Headers: map[string]string{"etag": `"e1"`}}, nil
	}
	if got := statusOf(refresh(false)); got != "slow="+statusDeferred {
		t.Fatalf("results %s", got)
	}
	row := f.feeds[f.keyOf("slow")]
	if len(f.items) != 0 || row.ETag != "" || !row.LastAttempt.IsZero() {
		t.Fatalf("must not store and must stay due: items %d, row %+v", len(f.items), row)
	}
}

func TestRefreshKeepsOldValidatorsWhenStoringFails(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	serve(f, fetchRes{Status: 200, Body: rss(2), Headers: map[string]string{"etag": `"e1"`}})
	refresh(false)
	f.clock = f.clock.Add(31 * time.Minute)
	f.fetch = func(string, map[string]string) (fetchRes, error) {
		f.itemsErr = errors.New("statement timeout") // storing fails after the download
		return fetchRes{Status: 200, Body: rss(3), Headers: map[string]string{"etag": `"e2"`}}, nil
	}
	refresh(false)
	if got := f.feeds[f.keyOf("alpha")].ETag; got != `"e1"` {
		t.Fatalf("etag %q, want the old one so the next fetch is not a 304", got)
	}
}
