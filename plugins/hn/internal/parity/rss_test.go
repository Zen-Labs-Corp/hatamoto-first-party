package parity

import (
	"encoding/json"
	"encoding/xml"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// pubRowsJSON is n public story rows; the first has an external url and a hostile title.
func pubRowsJSON(n int) string {
	rows := []map[string]any{}
	for i := 0; i < n; i++ {
		row := map[string]any{"item_no": 900 - i, "kind": "story", "title": "T", "url": "", "body": "", "by": "bob", "points": 3, "comment_count": 1, "created_at": "2026-11-25T11:00:00Z"}
		if i == 0 {
			row["title"], row["url"], row["body"] = `<b>&"x"</b>`, "https://example.com/a?x=1&y=2", "text <i>&</i>"
		}
		rows = append(rows, row)
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

// pubDB answers the feed queries with n rows and the item query with item (or no row).
func pubDB(n int, item string) *fakeDB {
	return &fakeDB{
		Users: map[string]map[string]any{"k1": user(1, "alice")},
		Query: func(sql string, _ []any) (any, error) {
			switch {
			case strings.Contains(sql, "jsonb_agg"):
				return map[string]any{"rows": [][]any{{pubRowsJSON(n)}}}, nil
			case sql == sqlPubItem && item != "":
				return map[string]any{"rows": [][]any{{item}}}, nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
	}
}

func get(t *testing.T, r *core.Registry, pattern string, pv map[string]string, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: "GET", Pattern: pattern, PathValues: pv, Query: query})
	if err != nil {
		t.Fatal(err)
	}
	return resp
}

func TestRSSFeeds(t *testing.T) {
	pinNow(t, savedNow)
	for _, name := range []string{"news", "newest", "ask", "show", "jobs", "best"} {
		db := pubDB(31, "")
		install(t, db)
		r := newRegistry(t, nil)
		resp := get(t, r, "/rss", nil, map[string][]string{"feed": {name}})
		if resp.Status != 200 || resp.Headers["Content-Type"] != "application/rss+xml; charset=utf-8" || resp.Headers["Cache-Control"] != "public, max-age=60" {
			t.Fatalf("%s: %d %v", name, resp.Status, resp.Headers)
		}
		var doc rssDoc
		if err := xml.Unmarshal([]byte(resp.Body), &doc); err != nil {
			t.Fatalf("%s: %v\n%s", name, err, resp.Body)
		}
		if doc.Version != "2.0" || len(doc.Channel.Items) != 30 || doc.Channel.Title != "Hatamoto News: "+name || doc.Channel.Link != "/"+name {
			t.Errorf("%s: %q %d items, channel %+v", name, doc.Version, len(doc.Channel.Items), doc.Channel)
		}
		if !strings.HasPrefix(resp.Body, `<?xml version="1.0" encoding="UTF-8"?>`+"\n<rss") {
			t.Errorf("%s: header %.60q", name, resp.Body)
		}
		it := doc.Channel.Items[0]
		if it.Title != `<b>&"x"</b>` || it.Link != "https://example.com/a?x=1&y=2" || it.Comments != "/item/900" || it.GUID.Value != "item-900" || it.GUID.IsPermaLink != "false" ||
			it.PubDate != "Wed, 25 Nov 2026 11:00:00 +0000" || it.Description != "text <i>&</i>" {
			t.Errorf("%s: item %+v", name, it)
		}
		if doc.Channel.Items[1].Link != "/item/899" {
			t.Errorf("%s: internal link %q", name, doc.Channel.Items[1].Link)
		}
		if strings.Contains(resp.Body, "<b>") || strings.Contains(resp.Body, "<i>") {
			t.Errorf("%s: unescaped markup", name)
		}
		if len(db.Queries) != 1 {
			t.Fatalf("%s: %d queries", name, len(db.Queries))
		}
		q := db.Queries[0]
		wantArgs := 2
		if name == "best" {
			wantArgs = 3
		}
		if len(q.Args) != wantArgs || q.Args[0] != float64(31) || q.Args[1] != float64(0) {
			t.Errorf("%s: args %v", name, q.Args)
		}
		if !strings.Contains(q.SQL, "NOT s.dead") || strings.Contains(q.SQL, "hidden") || strings.Contains(q.SQL, "story_votes") || strings.Contains(q.SQL, "commenter_key") {
			t.Errorf("%s: sql %s", name, q.SQL)
		}
	}
}

func TestRSSDefaultsAndRefusals(t *testing.T) {
	pinNow(t, savedNow)
	db := pubDB(2, "")
	install(t, db)
	r := newRegistry(t, nil)
	resp := get(t, r, "/rss", nil, nil)
	var doc rssDoc
	if err := xml.Unmarshal([]byte(resp.Body), &doc); err != nil || doc.Channel.Title != "Hatamoto News: news" || len(doc.Channel.Items) != 2 {
		t.Errorf("default feed: %v %+v", err, doc.Channel)
	}
	db.Queries = nil
	for _, bad := range []string{"bogus", "NEWS", "news; DROP", "../x"} {
		resp = get(t, r, "/rss", nil, map[string][]string{"feed": {bad}})
		if resp.Status != 404 {
			t.Errorf("feed %q: %d", bad, resp.Status)
		}
	}
	if len(db.Queries) != 0 {
		t.Errorf("unknown feeds queried %v", db.Queries)
	}
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	if resp = get(t, r, "/rss", nil, nil); resp.Status != 503 {
		t.Errorf("host error: %d", resp.Status)
	}
}
