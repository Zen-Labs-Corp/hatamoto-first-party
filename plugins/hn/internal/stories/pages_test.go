package stories

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// pageDB answers a feed with n rows and a story (item and ref queries) built from story.
func pageDB(n int, story map[string]any) *fakeDB {
	row := map[string]any{"entity_id": entityID, "item_no": 42, "author_id": 2, "by": "bob", "kind": "story", "dead": false, "points": 5, "comment_count": 0,
		"created_at": "2026-11-25T11:00:00Z", "title": "T", "url": "https://example.com/a", "domain": "example.com", "body": "", "voted_at": nil}
	for k, v := range story {
		if k != "held_comments" {
			row[k] = v
		}
	}
	b, _ := json.Marshal(row)
	return &fakeDB{
		Users: map[string]map[string]any{"k1": user(1, "alice"), "k2": user(2, "bob")},
		Query: func(sql string, _ []any) (any, error) {
			switch {
			case strings.Contains(sql, "FROM comment_meta"):
				return map[string]any{"rows": [][]any{{story["held_comments"] == true}}}, nil
			case strings.Contains(sql, "jsonb_agg"):
				return map[string]any{"rows": [][]any{{rowsJSON(n, 900)}}}, nil
			case strings.Contains(sql, "WHERE s.item_no = $1"):
				return map[string]any{"rows": [][]any{{string(b)}}}, nil
			}
			return map[string]any{"rows": [][]any{}}, nil
		},
	}
}

func render(t *testing.T, r *core.Registry, path, key string, query map[string]string, blocks ...sdk.Block) sdk.PageContext {
	t.Helper()
	out, err := r.RenderPage(sdk.PageContext{Path: path, Query: query, CommenterKey: key, Blocks: blocks})
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func TestFeedsPageReplacesBlock(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(3, nil))
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", nil, sdk.Block{ID: "b1", Type: "hn_userbar"}, sdk.Block{ID: "b7", Type: "hn_feed", Title: "T"})
	if len(out.Blocks) != 2 || out.Blocks[0].Type != "hn_userbar" || out.Blocks[1].ID != "b7" || out.Blocks[1].Title != "T" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	c := out.Blocks[1].Content
	if rows, _ := c["rows"].([]map[string]any); len(rows) != 3 || rows[0]["rank"] != "1." || rows[2]["rank"] != "3." {
		t.Errorf("rows %v", c["rows"])
	}
	if c["empty"] != false || c["more"] != "" {
		t.Errorf("content %v", c)
	}
}

// pinnedDB answers the pinned query (array_position) with pinned and the ranked feed with n rows.
func pinnedDB(n int, pinned string, pinErr bool) *fakeDB {
	db := pageDB(n, nil)
	db.Settings = map[string]any{"pinned_items": "1"}
	db.Query = func(sql string, _ []any) (any, error) {
		switch {
		case strings.Contains(sql, "array_position"):
			if pinErr {
				return nil, hostErr("db_error")
			}
			return map[string]any{"rows": [][]any{{pinned}}}, nil
		case strings.Contains(sql, "jsonb_agg"):
			return map[string]any{"rows": [][]any{{rowsJSON(n, 900)}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}
	return db
}

func TestFeedsPagePinned(t *testing.T) {
	pinNow(t, feedNow)
	pin := `[{"entity_id":"` + entityID + `","item_no":1,"author_id":2,"by":"bob","kind":"story","dead":false,"points":9,"comment_count":0,"created_at":"2026-11-25T11:00:00Z","title":"Welcome","url":"","domain":"","voted_at":null}]`
	r := newRegistry(t, nil)
	rowsOf := func(db *fakeDB, q map[string]string) []map[string]any {
		install(t, db)
		out := render(t, r, "/news", "k1", q, sdk.Block{ID: "b7", Type: "hn_feed"})
		return out.Blocks[0].Content["rows"].([]map[string]any)
	}
	rows := rowsOf(pinnedDB(2, pin, false), nil)
	if len(rows) != 3 || rows[0]["no"] != "1" || rows[0]["rank"] != "" || rows[0]["score"] == "" || rows[1]["rank"] != "1." || rows[2]["rank"] != "2." {
		t.Errorf("page 1 rows %v", rows)
	}
	if rows = rowsOf(pinnedDB(2, pin, false), map[string]string{"p": "2"}); len(rows) != 2 || rows[0]["rank"] != "31." {
		t.Errorf("page 2 rows %v", rows)
	}
	if rows = rowsOf(pinnedDB(2, pin, true), nil); len(rows) != 2 || rows[0]["rank"] != "1." {
		t.Errorf("pin error rows %v", rows)
	}
}

func TestFeedsPageBestKeepsView(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(3, nil))
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "k1", map[string]string{"view": "best", "p": "2"}, sdk.Block{ID: "b7", Type: "hn_feed"})
	rows, _ := out.Blocks[len(out.Blocks)-1].Content["rows"].([]map[string]any)
	if len(rows) == 0 || !strings.Contains(rows[0]["next"].(string), "view=best") {
		t.Errorf("rows %v", rows)
	}
}

func TestFeedsPageAppendsAndRoot(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(1, nil))
	r := newRegistry(t, nil)
	out := render(t, r, "/news", "", map[string]string{"p": "1"}, sdk.Block{ID: "b1", Type: "hn_userbar"})
	if len(out.Blocks) != 2 || out.Blocks[1].Type != "hn_feed" || out.Blocks[1].ID != "hn_feed" {
		t.Fatalf("not appended last: %+v", out.Blocks)
	}
	if rows := out.Blocks[1].Content["rows"].([]map[string]any); rows[0]["next"] != "/news?p=1#"+rows[0]["no"].(string) {
		t.Errorf("next %v", rows[0]["next"])
	}
	blocks := []sdk.Block{{ID: "b1", Type: "hn_userbar"}}
	if out = render(t, r, "/", "", nil, blocks...); !reflect.DeepEqual(out.Blocks, blocks) {
		t.Errorf("/ without hn_feed changed the page: %+v", out.Blocks)
	}
	if out = render(t, r, "/", "", nil, sdk.Block{ID: "b2", Type: "hn_feed"}); out.Blocks[0].Content == nil {
		t.Error("/ did not fill an existing hn_feed")
	}
}

func TestFeedsPageFailsOpen(t *testing.T) {
	r := newRegistry(t, nil)
	if out := render(t, r, "/news", "", nil); len(out.Blocks) != 0 {
		t.Errorf("no host: %+v", out.Blocks)
	}
	db := pageDB(1, nil)
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	install(t, db)
	if out := render(t, r, "/news", "", nil); len(out.Blocks) != 0 {
		t.Errorf("db error: %+v", out.Blocks)
	}
	p := &core.Page{Ctx: &sdk.PageContext{Path: "/news"}}
	if err := feedPage(r, p); err != nil || len(p.Ctx.Blocks) != 0 {
		t.Errorf("handler: %v %v", err, p.Ctx.Blocks)
	}
}

func TestFeedsPageNotice(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(0, nil))
	r := newRegistry(t, nil)
	if c := render(t, r, "/news", "", map[string]string{"m": "story-saved"}).Blocks[0].Content; c["notice"] != "Saved." || c["empty"] != true {
		t.Errorf("content %v", c)
	}
	if c := render(t, r, "/news", "", map[string]string{"m": "<script>"}).Blocks[0].Content; c["notice"] != "" {
		t.Errorf("raw code echoed: %v", c["notice"])
	}
}

func TestItemPage(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(0, map[string]any{"body": "first\n\n  \n\nsecond ", "url": ""}))
	r := newRegistry(t, nil)
	c := render(t, r, "/item/42", "k1", nil).Blocks[0].Content
	if paras := c["paras"].([]string); !reflect.DeepEqual(paras, []string{"first", "second"}) {
		t.Errorf("paras %v", paras)
	}
	if row := c["row"].(map[string]any); row["rank"] != "" || row["next"] != "/item/42#42" || row["vote"] != "up" {
		t.Errorf("row %v", row)
	}
	if out := render(t, r, "/item/42/", "k1", nil); len(out.Blocks) != 1 {
		t.Errorf("trailing slash: %+v", out.Blocks)
	}
	for _, path := range []string{"/item/abc", "/item/0"} {
		if out := render(t, r, path, "k1", nil); len(out.Blocks) != 0 {
			t.Errorf("%s: %+v", path, out.Blocks)
		}
	}
}

func TestItemPageDead(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(0, map[string]any{"dead": true}))
	r := newRegistry(t, nil)
	if c := render(t, r, "/item/42", "k1", nil).Blocks[0].Content; c["gone"] != true || c["row"] != nil {
		t.Errorf("other viewer: %v", c)
	}
	if c := render(t, r, "/item/42", "", nil).Blocks[0].Content; c["gone"] != true {
		t.Errorf("signed out: %v", c)
	}
	if c := render(t, r, "/item/42", "k2", nil).Blocks[0].Content; c["gone"] == true || c["row"] == nil {
		t.Errorf("author: %v", c)
	}
	db := pageDB(0, map[string]any{"dead": true})
	db.Users["k1"]["showdead"] = true
	install(t, db)
	if c := render(t, r, "/item/42", "k1", nil).Blocks[0].Content; c["gone"] == true {
		t.Errorf("showdead viewer: %v", c)
	}
}

func TestItemPageMissing(t *testing.T) {
	db := pageDB(0, nil)
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{}}, nil }
	install(t, db)
	r := newRegistry(t, nil)
	if out := render(t, r, "/item/42", "k1", nil); len(out.Blocks) != 0 {
		t.Errorf("%+v", out.Blocks)
	}
}

func TestSubmitPage(t *testing.T) {
	pinNow(t, feedNow)
	install(t, pageDB(0, map[string]any{"body": "mine"}))
	r := newRegistry(t, nil)
	c := render(t, r, "/submit", "", nil).Blocks[0].Content
	if c["state"] != "login" || c["login_action"] != "/p/hn/login" || c["signup_action"] != "/p/hn/signup" || c["next"] != "/submit" {
		t.Errorf("signed out %v", c)
	}
	if _, ok := c["login_href"]; ok {
		t.Errorf("signed out has login_href: %v", c)
	}
	if c := render(t, r, "/submit", "k9", nil).Blocks[0].Content; c["state"] != "noname" || c["login_href"] != "/login" || c["login_action"] != nil {
		t.Errorf("signed in without a username %v", c)
	}
	if c := render(t, r, "/submit", "k1", nil).Blocks[0].Content; c["state"] != "new" || c["action"] != "/p/hn/submit" {
		t.Errorf("new %v", c)
	}
	c = render(t, r, "/submit", "k2", map[string]string{"id": "42"}).Blocks[0].Content
	if c["state"] != "edit" || c["title"] != "T" || c["url"] != "https://example.com/a" || c["text"] != "mine" || c["can_delete"] != true ||
		c["action"] != "/p/hn/story/42/edit" || c["delete_action"] != "/p/hn/story/42/delete" {
		t.Errorf("edit %v", c)
	}
	install(t, pageDB(0, map[string]any{"held_comments": true}))
	if c = render(t, r, "/submit", "k2", map[string]string{"id": "42"}).Blocks[0].Content; c["state"] != "edit" || c["can_delete"] != false {
		t.Errorf("held comments %v", c)
	}
	if c = render(t, r, "/submit", "k1", map[string]string{"id": "42"}).Blocks[0].Content; c["state"] != "denied" || c["reason"] != "Only the author can change this story." {
		t.Errorf("other viewer %v", c)
	}
	if c = render(t, r, "/submit", "k1", map[string]string{"id": "x"}).Blocks[0].Content; c["state"] != "denied" {
		t.Errorf("bad id %v", c)
	}
}
