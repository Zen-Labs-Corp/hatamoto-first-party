package parity

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

var savedNow = mustTime("2026-11-25T12:00:00Z")

func storyRowsJSON(n int) string {
	rows := []map[string]any{}
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{"entity_id": storyUUID, "item_no": 900 - i, "author_id": 2, "by": "bob", "title": "T", "kind": "story",
			"points": 5, "comment_count": 2, "created_at": "2026-11-25T11:00:00Z", "url": "https://example.com/a", "domain": "example.com"})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func commentRowsJSON(n int) string {
	rows := []map[string]any{}
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{"comment_id": commentUUID, "entity_id": storyUUID, "item_no": 42, "by": "carol", "story_title": "S <b>",
			"excerpt": "hello", "created_at": "2026-11-25T10:00:00Z"})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

// favDB answers the list queries with the given JSON and the row state with fav.
func favDB(list string, fav bool) *fakeDB {
	db := &fakeDB{Users: map[string]map[string]any{"k1": user(1, "alice"), "k2": user(2, "bob")}}
	db.Query = func(sql string, _ []any) (any, error) {
		switch sql {
		case sqlFavStories, sqlHiddenStories, sqlFavComments:
			return map[string]any{"rows": [][]any{{list}}}, nil
		case sqlState:
			if fav {
				return map[string]any{"rows": [][]any{{`{"fav":true,"hid":false}`}}}, nil
			}
			return map[string]any{"rows": [][]any{{`{"fav":false,"hid":false}`}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}
	return db
}

func listQueries(db *fakeDB) (out []sqlCall) {
	for _, q := range db.Queries {
		if q.SQL != sqlState {
			out = append(out, q)
		}
	}
	return out
}

func TestFavoritesPagePlacesBlock(t *testing.T) {
	pinNow(t, savedNow)
	db := favDB(storyRowsJSON(3), true)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "k1", map[string]string{"id": "bob", "p": "2", "m": "saved-error"},
		sdk.Block{ID: "b1", Type: "hn_userbar"}, sdk.Block{ID: "b7", Type: "hn_saved", Title: "Saved"})
	if len(out.Blocks) != 2 || out.Blocks[1].ID != "b7" || out.Blocks[1].Title != "Saved" || out.Blocks[1].Type != "hn_saved" {
		t.Fatalf("blocks %+v", out.Blocks)
	}
	c := out.Blocks[1].Content
	rows, _ := c["rows"].([]map[string]any)
	if c["state"] != "list" || c["heading"] != "bob's favorites" || len(rows) != 3 || c["empty"] != false || c["more"] != "" {
		t.Fatalf("content %v", c)
	}
	if rows[0]["rank"] != "31." || rows[0]["next"] != "/favorites?id=bob&p=2" {
		t.Errorf("row %v", rows[0])
	}
	if c["notice"] != "Something went wrong, please try again." {
		t.Errorf("notice %v", c["notice"])
	}
	acts, _ := rows[0]["actions"].([]map[string]any)
	if len(acts) != 2 || acts[1]["label"] != "unfavorite" {
		t.Errorf("actions %v", acts)
	}
	qs := listQueries(db)
	if len(qs) != 1 || qs[0].SQL != sqlFavStories {
		t.Fatalf("queries %v", qs)
	}
	if a := qs[0].Args; a[0] != float64(1) || a[1] != float64(2) || a[2] != float64(31) || a[3] != float64(30) {
		t.Errorf("args %v", a)
	}

	// no block on the page: appended, never prepended
	out = render(t, r, "/favorites", "k1", map[string]string{"id": "bob"}, sdk.Block{ID: "b1", Type: "hn_userbar"})
	if len(out.Blocks) != 2 || out.Blocks[0].Type != "hn_userbar" || out.Blocks[1].Type != "hn_saved" {
		t.Errorf("blocks %+v", out.Blocks)
	}
}

func TestFavoritesPageMore(t *testing.T) {
	pinNow(t, savedNow)
	install(t, favDB(storyRowsJSON(31), false))
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "", map[string]string{"id": "bob"}, sdk.Block{ID: "b7", Type: "hn_saved"})
	c := out.Blocks[0].Content
	if rows, _ := c["rows"].([]map[string]any); len(rows) != 30 || c["more"] != "/favorites?id=bob&p=2" {
		t.Errorf("content %v", c)
	}
}

func TestFavoritesPageOwnAndLogin(t *testing.T) {
	pinNow(t, savedNow)
	db := favDB(storyRowsJSON(1), false)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "k1", nil, sdk.Block{ID: "b7", Type: "hn_saved"})
	if c := out.Blocks[0].Content; c["heading"] != "alice's favorites" {
		t.Errorf("own list: %v", c)
	}
	if qs := listQueries(db); qs[0].Args[1] != float64(1) {
		t.Errorf("owner arg %v", qs[0].Args)
	}
	db.Queries = nil
	out = render(t, r, "/favorites", "", nil, sdk.Block{ID: "b7", Type: "hn_saved"})
	if c := out.Blocks[0].Content; c["state"] != "login" || c["login_href"] != "/login?m=saved-login" {
		t.Errorf("signed out: %v", c)
	}
	if len(listQueries(db)) != 0 {
		t.Errorf("signed out ran %v", db.Queries)
	}
}

func TestFavoritesPageComments(t *testing.T) {
	pinNow(t, savedNow)
	db := favDB(commentRowsJSON(2), false)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "", map[string]string{"id": "bob", "kind": "comment"}, sdk.Block{ID: "b7", Type: "hn_saved"})
	c := out.Blocks[0].Content
	cs, _ := c["comments"].([]map[string]any)
	if c["kind"] != "comment" || c["heading"] != "bob's favorite comments" || len(cs) != 2 || cs[0]["item_href"] != "/item/42#"+commentUUID || cs[0]["excerpt"] != "hello" {
		t.Fatalf("content %v", c)
	}
	qs := listQueries(db)
	if len(qs) != 1 || qs[0].SQL != sqlFavComments || len(qs[0].Args) != 3 || qs[0].Args[0] != float64(2) || qs[0].Args[1] != float64(31) {
		t.Errorf("queries %v", qs)
	}
	if strings.Contains(sqlFavComments, "body_html") {
		t.Error("comment list selects the HTML body")
	}
}

func TestFavoritesPageHidden(t *testing.T) {
	pinNow(t, savedNow)
	db := favDB(storyRowsJSON(2), false)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "", map[string]string{"view": "hidden"}, sdk.Block{ID: "b7", Type: "hn_saved"})
	if c := out.Blocks[0].Content; c["state"] != "login" || len(listQueries(db)) != 0 {
		t.Errorf("signed out: %v, queries %v", c, db.Queries)
	}
	out = render(t, r, "/favorites", "k1", map[string]string{"view": "hidden", "id": "bob"}, sdk.Block{ID: "b7", Type: "hn_saved"})
	c := out.Blocks[0].Content
	qs := listQueries(db)
	if len(qs) != 1 || qs[0].SQL != sqlHiddenStories || qs[0].Args[1] != float64(1) || c["heading"] != "Hidden stories" || c["stories_href"] != "" {
		t.Errorf("hidden: %v, queries %v", c, qs)
	}
}

func TestFavoritesPageUnknownUserAndErrors(t *testing.T) {
	pinNow(t, savedNow)
	db := favDB(storyRowsJSON(1), false)
	install(t, db)
	r := newRegistry(t, nil)
	out := render(t, r, "/favorites", "k1", map[string]string{"id": "nobody"}, sdk.Block{ID: "b7", Type: "hn_saved"})
	if c := out.Blocks[0].Content; c["state"] != "nouser" {
		t.Errorf("unknown user: %v", c)
	}
	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	blocks := []sdk.Block{{ID: "b7", Type: "hn_saved", Title: "x"}}
	out = render(t, r, "/favorites", "k1", map[string]string{"id": "bob"}, blocks...)
	if len(out.Blocks) != 1 || out.Blocks[0].Content != nil || out.Blocks[0].Title != "x" {
		t.Errorf("query error changed the page: %+v", out.Blocks)
	}
}
