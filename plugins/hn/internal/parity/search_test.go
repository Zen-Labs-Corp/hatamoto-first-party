package parity

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// hitsJSON is n story hits followed by m comment hits, as sqlSearch returns them.
func hitsJSON(n, m int) string {
	rows := []map[string]any{}
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{"grp": 0, "rank": 0.5, "entity_id": storyUUID, "item_no": 900 - i, "author_id": 2, "by": "bob", "title": "T", "kind": "story",
			"url": "https://example.com/a", "domain": "example.com", "points": 3, "comment_count": 1, "dead": false, "created_at": "2026-11-25T11:00:00Z", "voted_at": nil, "comment_id": nil, "excerpt": ""})
	}
	for i := 0; i < m; i++ {
		rows = append(rows, map[string]any{"grp": 1, "rank": 0.4, "entity_id": storyUUID, "item_no": 42, "author_id": 3, "by": "carol", "title": "S", "kind": "story",
			"url": "", "domain": "", "points": 0, "comment_count": 0, "dead": false, "created_at": "2026-11-25T10:00:00Z", "voted_at": nil, "comment_id": commentUUID, "excerpt": "found <b>it</b>"})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func searchDB(list string) *fakeDB {
	db := &fakeDB{Users: map[string]map[string]any{"k1": user(1, "alice")}}
	db.Query = func(sql string, _ []any) (any, error) {
		if sql == sqlSearch {
			return map[string]any{"rows": [][]any{{list}}}, nil
		}
		return map[string]any{"rows": [][]any{{`{"fav":false,"hid":false}`}}}, nil
	}
	return db
}

func searchQueries(db *fakeDB) (out []sqlCall) {
	for _, q := range db.Queries {
		if q.SQL == sqlSearch {
			out = append(out, q)
		}
	}
	return out
}

func TestSearchQueryEmptyShowsFormOnly(t *testing.T) {
	pinNow(t, savedNow)
	db := searchDB("[]")
	h := install(t, db)
	r := newRegistry(t, nil)
	for _, q := range []string{"", "   ", "\t\n"} {
		out := render(t, r, "/search", "", map[string]string{"q": q}, sdk.Block{ID: "b1", Type: "hn_userbar"}, sdk.Block{ID: "b9", Type: "hn_search", Title: "Find"})
		if len(out.Blocks) != 2 || out.Blocks[1].ID != "b9" || out.Blocks[1].Title != "Find" || out.Blocks[1].Content["q"] != "" || out.Blocks[1].Content["empty"] != false {
			t.Fatalf("q %q: blocks %+v", q, out.Blocks)
		}
	}
	if len(db.Queries) != 0 || len(h.Calls) != 0 {
		t.Errorf("empty query hit the host: %v %v", db.Queries, h.Calls)
	}
	out := render(t, r, "/search", "", nil, sdk.Block{ID: "b1", Type: "hn_userbar"})
	if len(out.Blocks) != 2 || out.Blocks[1].Type != "hn_search" {
		t.Errorf("appended block %+v", out.Blocks)
	}
}

func TestSearchQueryRunsOneStatement(t *testing.T) {
	pinNow(t, savedNow)
	db := searchDB(hitsJSON(2, 1))
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/search", "k1", map[string]string{"q": "wasm"}, sdk.Block{ID: "b9", Type: "hn_search"})
	qs := searchQueries(db)
	if len(qs) != 1 || len(qs[0].Args) != 4 || qs[0].Args[0] != "wasm" || qs[0].Args[1] != float64(1) || qs[0].Args[2] != float64(31) || qs[0].Args[3] != float64(0) {
		t.Fatalf("queries %v", qs)
	}
	c := out.Blocks[0].Content
	rows, _ := c["rows"].([]map[string]any)
	cs, _ := c["comments"].([]map[string]any)
	if len(rows) != 2 || len(cs) != 1 || c["empty"] != false || c["more"] != "" || rows[0]["rank"] != "1." || rows[0]["next"] != "/search?q=wasm" {
		t.Fatalf("content %v", c)
	}
	if cs[0]["item_href"] != "/item/42#"+commentUUID || cs[0]["excerpt"] != "found <b>it</b>" || cs[0]["story_title"] != "S" {
		t.Errorf("comment %v", cs[0])
	}
	// signed out: viewer id 0
	db.Queries = nil
	render(t, r, "/search", "", map[string]string{"q": "wasm"}, sdk.Block{ID: "b9", Type: "hn_search"})
	if qs := searchQueries(db); qs[0].Args[1] != float64(0) {
		t.Errorf("signed out args %v", qs[0].Args)
	}
}

func TestSearchQueryPaging(t *testing.T) {
	pinNow(t, savedNow)
	db := searchDB(hitsJSON(31, 0))
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/search", "", map[string]string{"q": "a b&c", "p": "2"}, sdk.Block{ID: "b9", Type: "hn_search"})
	c := out.Blocks[0].Content
	rows, _ := c["rows"].([]map[string]any)
	if len(rows) != 30 || c["more"] != "/search?q=a+b%26c&p=3" || rows[0]["rank"] != "31." {
		t.Errorf("content more %v rows %d first %v", c["more"], len(rows), rows[0]["rank"])
	}
	if qs := searchQueries(db); qs[0].Args[3] != float64(30) {
		t.Errorf("offset %v", qs[0].Args)
	}
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{{"[]"}}}, nil }
	out = render(t, r, "/search", "", map[string]string{"q": "zzz"}, sdk.Block{ID: "b9", Type: "hn_search"})
	if out.Blocks[0].Content["empty"] != true {
		t.Errorf("no hits: %v", out.Blocks[0].Content)
	}
}

func TestSearchQueryIsCappedAndBound(t *testing.T) {
	for _, tc := range []string{strings.Repeat("a", 150), strings.Repeat("a", 99) + "é", strings.Repeat("é", 80), "  padded  "} {
		got := cleanQ(tc)
		if len(got) > maxQ || !utf8.ValidString(got) || got != strings.TrimSpace(got) {
			t.Errorf("cleanQ(%q...) = %q (%d bytes)", tc[:min(len(tc), 8)], got, len(got))
		}
	}
	if cleanQ(strings.Repeat("a", 99)+"é") != strings.Repeat("a", 99) {
		t.Error("a rune straddling the cap was kept")
	}
	pinNow(t, savedNow)
	db := searchDB("[]")
	install(t, db)
	r := newRegistry(t, nil)
	evil := `'); DROP TABLE x; --`
	render(t, r, "/search", "", map[string]string{"q": evil}, sdk.Block{ID: "b9", Type: "hn_search"})
	qs := searchQueries(db)
	if len(qs) != 1 || qs[0].Args[0] != evil || strings.Contains(qs[0].SQL, "DROP") {
		t.Errorf("queries %v", qs)
	}
	render(t, r, "/search", "", map[string]string{"q": strings.Repeat("x", 150)}, sdk.Block{ID: "b9", Type: "hn_search"})
	if a, _ := searchQueries(db)[1].Args[0].(string); len(a) != 100 {
		t.Errorf("long arg %d bytes", len(a))
	}
	low := strings.ToLower(sqlSearch)
	for _, bad := range []string{"pg_", "public", "ts_stat", "ts_rewrite", "ilike", "information_schema", "plainto_tsquery"} {
		if strings.Contains(low, bad) {
			t.Errorf("sqlSearch contains %q", bad)
		}
	}
	if !strings.Contains(sqlSearch, "NOT EXISTS (SELECT 1 FROM hidden h WHERE h.story_id = s.entity_id AND h.user_id = $2)") {
		t.Error("hidden filter missing")
	}
}

func TestSearchQueryHostError(t *testing.T) {
	pinNow(t, savedNow)
	db := searchDB("[]")
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/search", "", map[string]string{"q": "x"}, sdk.Block{ID: "b9", Type: "hn_search", Title: "t"})
	if len(out.Blocks) != 1 || out.Blocks[0].Content != nil {
		t.Errorf("page changed: %+v", out.Blocks)
	}
}

func TestSearchQuerySection(t *testing.T) {
	pinNow(t, savedNow)
	install(t, searchDB("[]"))
	r := newRegistry(t, nil)
	s := storyRow{EntityID: storyUUID, ItemNo: 42, AuthorID: 2, By: "bob", Title: "T", URL: "https://example.com/a", Domain: "example.com", Kind: "story", Points: 3, CreatedAt: savedNow.Add(-time.Hour)}
	cm := commentView(commentRow{CommentID: commentUUID, ItemNo: 42, By: "carol", StoryTitle: "S", Excerpt: "<script>x</script>", CreatedAt: savedNow.Add(-time.Hour)})
	html := run(t, "section-hn_search", map[string]any{"q": `"><script>alert(1)</script>`, "action": "/search", "rows": []map[string]any{storyView(r, nil, s, 1, "/search?q=x")},
		"comments": []map[string]any{cm}, "more": "/search?q=x&p=2", "empty": false})
	for _, want := range []string{`<form class="hn-search-form" method="get" action="/search">`, `name="q"`, `maxlength="100"`, `&#34;&gt;&lt;script&gt;alert(1)&lt;/script&gt;`,
		`href="https://example.com/a"`, `&lt;script&gt;x&lt;/script&gt;`, `href="/search?q=x&amp;p=2" rel="next"`} {
		if !strings.Contains(html, want) {
			t.Errorf("search lacks %q:\n%s", want, html)
		}
	}
	if strings.Contains(html, "<script") || strings.Contains(html, "No results.") {
		t.Errorf("bad output: %s", html)
	}
	if h := run(t, "section-hn_search", map[string]any{"q": "zzz", "action": "/search", "rows": []map[string]any{}, "comments": []map[string]any{}, "empty": true}); !strings.Contains(h, "No results.") {
		t.Errorf("empty: %s", h)
	}
}

func TestSearchNoMoreAtPageCap(t *testing.T) {
	pinNow(t, savedNow)
	install(t, searchDB(hitsJSON(31, 0)))
	r := newRegistry(t, nil)
	out := render(t, r, "/search", "", map[string]string{"q": "a", "p": "500"}, sdk.Block{ID: "b9", Type: "hn_search"})
	if more := out.Blocks[0].Content["more"]; more != "" {
		t.Errorf("more at the page cap: %v", more)
	}
}
