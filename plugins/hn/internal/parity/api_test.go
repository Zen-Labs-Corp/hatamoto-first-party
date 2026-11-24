package parity

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

func keysOf(t *testing.T, raw string) []string {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(raw), &m); err != nil {
		t.Fatalf("%v: %s", err, raw)
	}
	var out []string
	for k := range m {
		out = append(out, k)
	}
	slices.Sort(out)
	return out
}

func TestAPIFeed(t *testing.T) {
	pinNow(t, savedNow)
	db := pubDB(31, "")
	install(t, db)
	r := newRegistry(t, nil)
	resp := get(t, r, "/api/feed/{name}", map[string]string{"name": "ask"}, map[string][]string{"p": {"2"}})
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "public, max-age=30" || resp.Headers["Content-Type"] != "application/json; charset=utf-8" {
		t.Fatalf("%d %v", resp.Status, resp.Headers)
	}
	if got := keysOf(t, resp.Body); !slices.Equal(got, []string{"has_more", "items", "page"}) {
		t.Errorf("keys %v", got)
	}
	var feed struct {
		Items   []map[string]any `json:"items"`
		Page    int              `json:"page"`
		HasMore bool             `json:"has_more"`
	}
	if err := json.Unmarshal([]byte(resp.Body), &feed); err != nil || len(feed.Items) != 30 || feed.Page != 2 || !feed.HasMore {
		t.Errorf("feed %v %d %d %v", err, len(feed.Items), feed.Page, feed.HasMore)
	}
	if db.Queries[0].Args[1] != float64(30) {
		t.Errorf("offset %v", db.Queries[0].Args)
	}
	if id, ok := feed.Items[0]["id"].(string); !ok || id != "900" {
		t.Errorf("id %v", feed.Items[0]["id"])
	}
	if resp = get(t, r, "/api/feed/{name}", map[string]string{"name": "nope"}, nil); resp.Status != 404 || resp.Headers["Cache-Control"] != "no-store" || resp.Body != `{"error":"not found"}` {
		t.Errorf("unknown feed: %d %v %s", resp.Status, resp.Headers, resp.Body)
	}
	// an empty feed is an empty list, never null
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{{"[]"}}}, nil }
	if resp = get(t, r, "/api/feed/{name}", map[string]string{"name": "news"}, nil); !strings.Contains(resp.Body, `"items":[]`) || strings.Contains(resp.Body, "null") {
		t.Errorf("empty feed: %s", resp.Body)
	}
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	if resp = get(t, r, "/api/feed/{name}", map[string]string{"name": "news"}, nil); resp.Status != 503 {
		t.Errorf("host error: %d", resp.Status)
	}
}

func TestAPIItem(t *testing.T) {
	pinNow(t, savedNow)
	item := `{"item_no":42,"kind":"ask","title":"Ask: x","url":"","body":"why","by":"bob","points":5,"comment_count":2,"created_at":"2026-11-25T11:00:00Z"}`
	db := pubDB(0, item)
	install(t, db)
	r := newRegistry(t, nil)
	resp := get(t, r, "/api/item/{id}", map[string]string{"id": "42"}, nil)
	want := []string{"by", "comments", "created_at", "id", "points", "text", "title", "type", "url"}
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "public, max-age=30" || !slices.Equal(keysOf(t, resp.Body), want) {
		t.Fatalf("%d %v %s", resp.Status, resp.Headers, resp.Body)
	}
	var it map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &it)
	if it["id"] != "42" || it["type"] != "ask" || it["text"] != "why" || it["comments"] != float64(2) || it["points"] != float64(5) {
		t.Errorf("item %v", it)
	}
	if !strings.Contains(sqlPubItem, "NOT s.dead") || db.Queries[0].Args[0] != float64(42) {
		t.Errorf("sql/args %v", db.Queries[0])
	}
	for _, id := range []string{"abc", "0", "-1", ""} {
		if resp = get(t, r, "/api/item/{id}", map[string]string{"id": id}, nil); resp.Status != 404 || resp.Headers["Cache-Control"] != "no-store" {
			t.Errorf("id %q: %d", id, resp.Status)
		}
	}
	// dead or unknown: the query returns no row
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{}}, nil }
	if resp = get(t, r, "/api/item/{id}", map[string]string{"id": "43"}, nil); resp.Status != 404 || resp.Body != `{"error":"not found"}` {
		t.Errorf("dead: %d %s", resp.Status, resp.Body)
	}
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	if resp = get(t, r, "/api/item/{id}", map[string]string{"id": "42"}, nil); resp.Status != 503 {
		t.Errorf("host error: %d", resp.Status)
	}
}

func TestAPIUser(t *testing.T) {
	pinNow(t, savedNow)
	row := user(2, "bob")
	row["about"], row["noprocrast"], row["showdead"], row["karma"] = "<a href=x>hi</a>", true, true, 77
	db := &fakeDB{Users: map[string]map[string]any{"k2": row}}
	install(t, db)
	r := newRegistry(t, nil)
	resp := get(t, r, "/api/user/{name}", map[string]string{"name": "Bob"}, nil)
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "public, max-age=30" {
		t.Fatalf("%d %v", resp.Status, resp.Headers)
	}
	if got := keysOf(t, resp.Body); !slices.Equal(got, []string{"about", "created_at", "karma", "name"}) {
		t.Errorf("keys %v: %s", got, resp.Body)
	}
	for _, leak := range []string{"k2", "commenter_key", "noprocrast", "showdead", "maxvisit", "minaway", "<a href"} {
		if strings.Contains(resp.Body, leak) {
			t.Errorf("body contains %q: %s", leak, resp.Body)
		}
	}
	if !strings.Contains(resp.Body, `\u003ca href=x\u003e`) || !strings.Contains(resp.Body, `"karma":77`) || !strings.Contains(resp.Body, `"name":"bob"`) {
		t.Errorf("body %s", resp.Body)
	}
	for _, name := range []string{"nobody", strings.Repeat("a", 33), ""} {
		if resp = get(t, r, "/api/user/{name}", map[string]string{"name": name}, nil); resp.Status != 404 || resp.Headers["Cache-Control"] != "no-store" {
			t.Errorf("name %q: %d", name, resp.Status)
		}
	}
	h := install(t, &fakeDB{})
	h.Fns["db.query"] = func(json.RawMessage) (any, error) { return nil, hostErr("db_error") }
	if resp = get(t, r, "/api/user/{name}", map[string]string{"name": "bob"}, nil); resp.Status != 503 {
		t.Errorf("host error: %d", resp.Status)
	}
}
