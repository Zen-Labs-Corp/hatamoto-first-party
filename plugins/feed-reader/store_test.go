package main

import (
	"fmt"
	"testing"
	"time"
)

func cfgOf(names ...string) []feedCfg {
	var out []feedCfg
	for i, n := range names {
		out = append(out, feedCfg{Key: "k-" + n, Name: n, URL: "https://example.com/" + n, MaxItems: 50, Group: i%2 + 1})
	}
	return out
}

func mkItems(n int, base time.Time) []item {
	var out []item
	for i := 0; i < n; i++ {
		out = append(out, item{GUID: fmt.Sprint("g", i), Title: fmt.Sprint("T", i), Link: fmt.Sprintf("https://example.com/%d", i),
			Published: base.Add(-time.Duration(i) * time.Hour)})
	}
	return out
}

func TestPruneKeepsNewestMaxItems(t *testing.T) {
	f := newFake(t)
	cfg := cfgOf("a")
	if err := syncFeeds(cfg); err != nil {
		t.Fatal(err)
	}
	if err := upsertItems("k-a", mkItems(30, f.clock)); err != nil {
		t.Fatal(err)
	}
	if err := prune("k-a", 10, f.clock); err != nil {
		t.Fatal(err)
	}
	got := f.itemsOf("k-a")
	if len(got) != 10 || got[0].GUID != "g0" || got[9].GUID != "g9" {
		t.Fatalf("kept %d, first %q last %q", len(got), got[0].GUID, got[len(got)-1].GUID)
	}
}

func TestPruneCapsAt200EvenWhenMaxItemsIsHigher(t *testing.T) {
	f := newFake(t)
	_ = syncFeeds(cfgOf("a"))
	// 250 items within the last 90 days.
	var items []item
	for i := 0; i < 250; i++ {
		items = append(items, item{GUID: fmt.Sprint("g", i), Title: "t", Link: "https://example.com/x", Published: f.clock.Add(-time.Duration(i) * time.Minute)})
	}
	_ = upsertItems("k-a", items)
	if err := prune("k-a", 500, f.clock); err != nil {
		t.Fatal(err)
	}
	if n := len(f.itemsOf("k-a")); n != 200 {
		t.Fatalf("kept %d, want 200", n)
	}
}

func TestPruneDeletesOlderThan90DaysByPublishedElseFetched(t *testing.T) {
	f := newFake(t)
	_ = syncFeeds(cfgOf("a"))
	old := f.clock.AddDate(0, 0, -91)
	recent := f.clock.AddDate(0, 0, -89)
	_ = upsertItems("k-a", []item{
		{GUID: "old", Title: "t", Link: "https://example.com/1", Published: old},
		{GUID: "recent", Title: "t", Link: "https://example.com/2", Published: recent},
		{GUID: "undated", Title: "t", Link: "https://example.com/3"}, // fetched now
	})
	// An undated item fetched 91 days ago is old too.
	f.items["k-a/stale"] = fakeItem{FeedKey: "k-a", GUID: "stale", FetchedAt: old}
	if err := prune("k-a", 50, f.clock); err != nil {
		t.Fatal(err)
	}
	var left []string
	for _, it := range f.itemsOf("k-a") {
		left = append(left, it.GUID)
	}
	if fmt.Sprint(left) != "[undated recent]" {
		t.Fatalf("left %v", left)
	}
}

func TestSyncFeedsRemovesUnconfiguredRows(t *testing.T) {
	f := newFake(t)
	_ = syncFeeds(cfgOf("a", "b"))
	_ = upsertItems("k-a", mkItems(2, f.clock))
	_ = upsertItems("k-b", mkItems(2, f.clock))
	if err := syncFeeds(cfgOf("a")); err != nil {
		t.Fatal(err)
	}
	if len(f.feeds) != 1 || len(f.itemsOf("k-b")) != 0 || len(f.itemsOf("k-a")) != 2 {
		t.Fatalf("feeds %d, b items %d, a items %d", len(f.feeds), len(f.itemsOf("k-b")), len(f.itemsOf("k-a")))
	}
	if err := syncFeeds(nil); err != nil || len(f.feeds) != 0 || len(f.items) != 0 {
		t.Fatalf("empty settings must clear everything: %v", err)
	}
}

func TestSyncKeepsStateAndUpdatesName(t *testing.T) {
	f := newFake(t)
	cfg := cfgOf("a")
	_ = syncFeeds(cfg)
	_ = saveState("k-a", "ok", `"e1"`, "Wed", "https://example.com/", 3, f.clock)
	cfg[0].Name, cfg[0].Group = "Renamed", 5
	_ = syncFeeds(cfg)
	r := f.feeds["k-a"]
	if r.Name != "Renamed" || r.Group != 5 || r.ETag != `"e1"` || r.Status != "ok" || r.ItemCount != 3 {
		t.Fatalf("row %+v", r)
	}
}

func TestUpsertItemsIdempotentAndKeepsFetchedAt(t *testing.T) {
	f := newFake(t)
	_ = syncFeeds(cfgOf("a"))
	items := mkItems(3, f.clock)
	items = append(items, items[0]) // duplicate guid in one batch
	if err := upsertItems("k-a", items); err != nil {
		t.Fatal(err)
	}
	first := f.clock
	f.clock = f.clock.Add(time.Hour)
	items[1].Title = "changed"
	if err := upsertItems("k-a", items); err != nil {
		t.Fatal(err)
	}
	got := f.itemsOf("k-a")
	if len(got) != 3 {
		t.Fatalf("items %d", len(got))
	}
	for _, it := range got {
		if !it.FetchedAt.Equal(first) {
			t.Fatalf("fetched_at moved: %v", it.FetchedAt)
		}
		if it.GUID == "g1" && it.Title != "changed" {
			t.Fatal("update not applied")
		}
	}
}

func TestDueFeedsOrderAndWindow(t *testing.T) {
	at := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	cfg := cfgOf("fresh", "old", "never", "older", "edge")
	rows := []feedRow{
		{Key: "k-fresh", LastAttempt: at.Add(-10 * time.Minute)},
		{Key: "k-old", LastAttempt: at.Add(-40 * time.Minute)},
		{Key: "k-older", LastAttempt: at.Add(-2 * time.Hour)},
		{Key: "k-edge", LastAttempt: at.Add(-30 * time.Minute)},
	}
	var got []string
	for _, f := range dueFeeds(cfg, rows, at, 30*time.Minute) {
		got = append(got, f.Name)
	}
	if fmt.Sprint(got) != "[never older old edge]" {
		t.Fatalf("due %v", got)
	}
}

func TestMarkDueMakesFeedDueAndFeedRowsRoundTrip(t *testing.T) {
	f := newFake(t)
	cfg := cfgOf("a", "b")
	_ = syncFeeds(cfg)
	_ = saveState("k-a", "ok", "", "", "", 0, f.clock)
	_ = saveState("k-b", "ok", "", "", "", 0, f.clock)
	if err := markDue([]string{"k-b"}); err != nil {
		t.Fatal(err)
	}
	rows, err := feedRows()
	if err != nil || len(rows) != 2 {
		t.Fatalf("rows %v %v", rows, err)
	}
	due := dueFeeds(cfg, rows, f.clock, 30*time.Minute)
	if len(due) != 1 || due[0].Key != "k-b" {
		t.Fatalf("due %+v", due)
	}
	if err := markDue(nil); err != nil {
		t.Fatal(err)
	}
}

func TestItemsForSourcesOrderAndLimit(t *testing.T) {
	f := newFake(t)
	cfg := cfgOf("a", "b") // a: group 1, b: group 2
	_ = syncFeeds(cfg)
	_ = upsertItems("k-a", []item{{GUID: "a1", Title: "A1", Link: "https://example.com/a1", Published: f.clock.Add(-2 * time.Hour)}})
	_ = upsertItems("k-b", []item{{GUID: "b1", Title: "B1", Link: "https://example.com/b1", Published: f.clock.Add(-time.Hour)},
		{GUID: "b2", Title: "B2", Link: "https://example.com/b2"}}) // undated: fetched now
	all, err := itemsFor("items", 0)
	if err != nil || len(all) != 3 || all[0].Title != "B2" || all[1].Title != "B1" || all[2].Title != "A1" {
		t.Fatalf("items %+v %v", all, err)
	}
	g2, _ := itemsFor("group-2", 1)
	if len(g2) != 1 || g2[0].Title != "B2" || g2[0].FeedName != "b" {
		t.Fatalf("group-2 %+v", g2)
	}
	if rows, err := itemsFor("group-8", 10); err != nil || len(rows) != 0 {
		t.Fatalf("unknown source: %v %v", rows, err)
	}
	if rows, _ := itemsFor("bogus", 10); len(rows) != 0 {
		t.Fatalf("unknown source: %v", rows)
	}
}

func TestSourceGroup(t *testing.T) {
	for src, want := range map[string]int{"items": 0, "group-1": 1, "group-7": 7} {
		if g, ok := sourceGroup(src); !ok || g != want {
			t.Fatalf("%s -> %d %v", src, g, ok)
		}
	}
	for _, src := range []string{"", "group-0", "group-8", "group-10", "group-", "Items", "group-a"} {
		if _, ok := sourceGroup(src); ok {
			t.Fatalf("%q must be unknown", src)
		}
	}
}

func TestDBErrorsAreReturned(t *testing.T) {
	f := newFake(t)
	f.dbDown = true
	if err := syncFeeds(cfgOf("a")); err == nil {
		t.Fatal("want error")
	}
	if _, err := feedRows(); err == nil {
		t.Fatal("want error")
	}
	if _, err := itemsFor("items", 5); err == nil {
		t.Fatal("want error")
	}
}

func TestUpsertFutureDatesDoNotMoveOnRefetch(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 0)
	items := []item{
		{GUID: "typo", Title: "typo", Link: "https://example.com/1", Published: f.clock.AddDate(36, 0, 0)},
		{GUID: "skew", Title: "skew", Link: "https://example.com/2", Published: f.clock.Add(3 * time.Hour)},
	}
	if err := upsertItems("a", items); err != nil {
		t.Fatal(err)
	}
	first := f.clock
	f.clock = f.clock.Add(31 * time.Minute)
	if err := upsertItems("a", items); err != nil {
		t.Fatal(err)
	}
	typo := f.items["a/typo"]
	if !typo.Published.IsZero() || !typo.FetchedAt.Equal(first) {
		t.Fatalf("a far-future date must not be stored or follow the refetch: published %v, fetched %v", typo.Published, typo.FetchedAt)
	}
	if got := f.items["a/skew"].Published; !got.Equal(first.Add(3 * time.Hour)) {
		t.Fatalf("a date within a day ahead must be kept: %v", got)
	}
}
