package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func runFilter(t *testing.T, queries ...sdk.BuilderDataQuery) sdk.BuilderDataRequest {
	t.Helper()
	in, _ := json.Marshal(sdk.BuilderDataRequest{Requests: queries})
	out, err := guest.Handle(sdk.BuilderData.Export(), in)
	if err != nil {
		t.Fatalf("filter error: %v", err)
	}
	var res sdk.BuilderDataRequest
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func feedRows(n int) []item {
	var rows []item
	for i := 1; i <= n; i++ {
		rows = append(rows, item{Source: "instagram", ID: fmt.Sprintf("p%02d", i), Position: i, Title: fmt.Sprintf("Post %d", i),
			Image: fmt.Sprintf("https://cdn.example/%d.jpg", i), URL: fmt.Sprintf("https://example.com/p/%d", i),
			Type: "image", PublishedAt: fmt.Sprintf("2026-01-%02dT00:00:00Z", i)})
	}
	return rows
}

func TestFilterNewestFirstWithImageAndAttrs(t *testing.T) {
	f := useFake(t)
	f.items = feedRows(3)
	res := runFilter(t, sdk.BuilderDataQuery{ID: "r1", Source: "ext.social-feeds.instagram"})
	if len(res.Results) != 1 || res.Results[0].ID != "r1" {
		t.Fatalf("results = %+v", res.Results)
	}
	items := res.Results[0].Items
	if len(items) != 3 || items[0].Title != "Post 3" || items[2].Title != "Post 1" {
		t.Fatalf("items = %+v", items)
	}
	if items[0].Image != "https://cdn.example/3.jpg" || items[0].URL != "https://example.com/p/3" || items[0].PublishedAt != "2026-01-03T00:00:00Z" {
		t.Errorf("item = %+v", items[0])
	}
	if a := items[0].Attrs; a["id"] != "p03" || a["type"] != "image" || a["source"] != "instagram" {
		t.Errorf("attrs = %v", a)
	}
	if f.fetchCount() != 0 {
		t.Error("the filter made an http call")
	}
	for _, c := range f.kvCalls {
		t.Errorf("the filter used kv: %s", c)
	}
}

func TestFilterCaps(t *testing.T) {
	f := useFake(t)
	f.items = feedRows(50)
	count := func(limit int) int {
		return len(runFilter(t, sdk.BuilderDataQuery{ID: "r", Source: "ext.social-feeds.instagram", Limit: limit}).Results[0].Items)
	}
	if got := count(0); got != 9 {
		t.Errorf("no settings, no limit = %d, want the default 9", got)
	}
	if got := count(4); got != 4 {
		t.Errorf("limit 4 = %d", got)
	}
	f.settings = `{"items":12}`
	if got := count(30); got != 12 {
		t.Errorf("items setting 12, limit 30 = %d", got)
	}
	f.settings = `{"items":48}`
	if got := count(100); got != 48 {
		t.Errorf("cap = %d, want 48", got)
	}
}

func TestFilterTruncatesOnRuneBoundary(t *testing.T) {
	f := useFake(t)
	long := strings.Repeat("é", 2000) // 4000 bytes
	f.items = []item{{Source: "youtube", ID: "v", Title: long, Excerpt: long, URL: long, Image: long, PublishedAt: "2026-01-01T00:00:00Z"}}
	it := runFilter(t, sdk.BuilderDataQuery{ID: "r", Source: "ext.social-feeds.youtube"}).Results[0].Items[0]
	for name, s := range map[string]string{"title": it.Title, "excerpt": it.Excerpt, "url": it.URL, "image": it.Image} {
		if len(s) > 2048 || !utf8.ValidString(s) || len(s) < 2046 {
			t.Errorf("%s: %d bytes, valid=%v", name, len(s), utf8.ValidString(s))
		}
	}
}

func TestFilterEmptyStates(t *testing.T) {
	f := useFake(t)
	res := runFilter(t,
		sdk.BuilderDataQuery{ID: "a", Source: "ext.social-feeds.youtube"},
		sdk.BuilderDataQuery{ID: "b", Source: "ext.social-feeds.nothing"})
	if len(res.Results) != 2 {
		t.Fatalf("results = %+v", res.Results)
	}
	for _, r := range res.Results {
		if len(r.Items) != 0 || r.Error != "" {
			t.Errorf("result %s = %+v", r.ID, r)
		}
	}
	if f.fetchCount() != 0 {
		t.Error("the filter made an http call")
	}
}

func TestFilterFailsOpenOnDatabaseError(t *testing.T) {
	f := useFake(t)
	f.items = feedRows(2)
	f.failDB = "FROM item"
	res := runFilter(t, sdk.BuilderDataQuery{ID: "r", Source: "ext.social-feeds.instagram"})
	if len(res.Results) != 0 || len(res.Requests) != 1 {
		t.Errorf("the request should come back unchanged: %+v", res)
	}
	if len(f.logs) == 0 {
		t.Error("the failure was not logged")
	}
}

func TestFilterHidesStaleInstagramRows(t *testing.T) {
	f := useFake(t)
	f.items = feedRows(2)
	q := sdk.BuilderDataQuery{ID: "r1", Source: "ext.social-feeds.instagram"}
	for age, want := range map[time.Duration]int{48 * time.Hour: 2, 73 * time.Hour: 0} {
		f.status["instagram"] = sourceStatus{Source: "instagram", RefreshedAt: f.clock.Add(-age).Format(time.RFC3339)}
		if got := len(runFilter(t, q).Results[0].Items); got != want {
			t.Fatalf("refreshed %v ago: %d items, want %d", age, got, want)
		}
	}
}
