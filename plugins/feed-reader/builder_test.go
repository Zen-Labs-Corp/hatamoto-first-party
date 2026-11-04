package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func addFeed(f *fakeHost, key, name string, group int) {
	f.feeds[key] = feedRow{Key: key, URL: "https://example.com/" + name + ".xml", Name: name, Group: group, SiteURL: "https://example.com/" + name + "/"}
}

func addItem(f *fakeHost, key, guid, title string, published time.Time) {
	f.items[key+"/"+guid] = fakeItem{FeedKey: key, GUID: guid, Title: title, Link: "https://example.com/" + guid,
		Excerpt: "excerpt " + guid, Published: published, FetchedAt: f.clock}
}

func ask(t *testing.T, source string, limit int) sdk.BuilderDataResult {
	t.Helper()
	out, err := handleBuilder(sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "r1", Source: source, Limit: limit}}})
	if err != nil || len(out.Results) != 1 || out.Results[0].ID != "r1" {
		t.Fatalf("err %v results %+v", err, out.Results)
	}
	return out.Results[0]
}

func titles(r sdk.BuilderDataResult) string {
	var t []string
	for _, it := range r.Items {
		t = append(t, it.Title)
	}
	return strings.Join(t, ",")
}

func TestBuilderItemsNewestFirstWithShape(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 1)
	addFeed(f, "b", "Beta", 2)
	day := func(d int) time.Time { return time.Date(2026, 11, d, 8, 0, 0, 0, time.UTC) }
	addItem(f, "a", "g1", "old", day(1))
	addItem(f, "b", "g2", "new", day(3))
	addItem(f, "a", "g3", "mid", day(2))
	res := ask(t, "ext.feed-reader.items", 10)
	if got := titles(res); got != "new,mid,old" {
		t.Fatalf("order %s", got)
	}
	it := res.Items[0]
	if it.URL != "https://example.com/g2" || it.Excerpt != "excerpt g2" || it.PublishedAt != "2026-11-03T08:00:00Z" ||
		it.Category != "Beta" || it.Attrs["source"] != "Beta" || it.Attrs["source_url"] != "https://example.com/Beta/" {
		t.Fatalf("item %+v", it)
	}
	if got := titles(ask(t, "ext.feed-reader.group-1", 10)); got != "mid,old" {
		t.Fatalf("group-1 %s", got)
	}
	if got := titles(ask(t, "ext.feed-reader.group-2", 1)); got != "new" {
		t.Fatalf("group-2 %s", got)
	}
}

func TestBuilderUnknownSourceAndEmptyDB(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 1)
	addItem(f, "a", "g1", "one", f.clock)
	for _, src := range []string{"ext.feed-reader.nope", "ext.feed-reader.group-8", "ext.feed-reader.group-0", "ext.feed-reader."} {
		if res := ask(t, src, 5); len(res.Items) != 0 || res.Error != "" {
			t.Fatalf("%s: %+v", src, res)
		}
	}
	g := newFake(t)
	if res := ask(t, "ext.feed-reader.items", 5); len(res.Items) != 0 || res.Error != "" {
		t.Fatalf("empty db: %+v (%v)", res, g.logs)
	}
}

func TestBuilderLimitCap(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 0)
	for i := 0; i < 60; i++ {
		addItem(f, "a", "g"+string(rune('A'+i/26))+string(rune('a'+i%26)), "t", f.clock.Add(-time.Duration(i)*time.Hour))
	}
	for _, limit := range []int{0, 100, 48} {
		if n := len(ask(t, "ext.feed-reader.items", limit).Items); n != 48 {
			t.Fatalf("limit %d gave %d", limit, n)
		}
	}
	if n := len(ask(t, "ext.feed-reader.items", 5).Items); n != 5 {
		t.Fatalf("limit 5 gave %d", n)
	}
}

func TestBuilderDBFailureSetsErrorAndKeepsHookOK(t *testing.T) {
	f := newFake(t)
	f.dbDown = true
	res := ask(t, "ext.feed-reader.items", 5)
	// An error keeps core from caching the empty loop; the hook itself
	// still succeeds (ask fails on a hook error) and the text is fixed.
	if len(res.Items) != 0 || res.Error != "unavailable" {
		t.Fatalf("%+v", res)
	}
	if !strings.Contains(f.logged(), "cannot read the items") || strings.Contains(f.logged(), "db unavailable") {
		t.Fatalf("log %q", f.logged())
	}
}

func TestBuilderClipsLongAndMultibyteStrings(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 0)
	addItem(f, "a", "g1", strings.Repeat("é", 3000), time.Time{})
	f.items["a/g1"] = func() fakeItem {
		it := f.items["a/g1"]
		it.Excerpt = strings.Repeat("x", 5*1024)
		return it
	}()
	addItem(f, "a", "g2", "bad \xff utf8", time.Time{})
	addItem(f, "a", "g3", "no link", time.Time{})
	it3 := f.items["a/g3"]
	it3.Link = "https://example.com/" + strings.Repeat("p", 2100)
	f.items["a/g3"] = it3
	res := ask(t, "ext.feed-reader.items", 10)
	if len(res.Items) != 2 {
		t.Fatalf("items %d (long link item must be skipped)", len(res.Items))
	}
	for _, it := range res.Items {
		for _, s := range []string{it.Title, it.Excerpt, it.Category, it.URL, it.Attrs["source"], it.Attrs["source_url"]} {
			if len(s) >= sdk.MaxBuilderDataString || !utf8.ValidString(s) {
				t.Fatalf("bad string len %d", len(s))
			}
		}
	}
}

func TestBuilderRoundTripThroughGuest(t *testing.T) {
	f := newFake(t)
	addFeed(f, "a", "Alpha", 0)
	addItem(f, "a", "g1", "one", f.clock)
	in, _ := json.Marshal(sdk.BuilderDataRequest{Requests: []sdk.BuilderDataQuery{{ID: "x", Source: "ext.feed-reader.items", Limit: 3}}})
	raw, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatal(err)
	}
	var out sdk.BuilderDataRequest
	if err := json.Unmarshal(raw, &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Results) != 1 || out.Results[0].ID != "x" || len(out.Results[0].Items) != 1 || out.Results[0].Items[0].Title != "one" {
		t.Fatalf("%s", raw)
	}
}
