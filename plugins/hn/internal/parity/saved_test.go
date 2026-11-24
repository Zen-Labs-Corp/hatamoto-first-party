package parity

import (
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	storyUUID   = "0b9a1f2e-3c4d-4e5f-8a6b-7c8d9e0f1a2b"
	commentUUID = "7c1d2e3f-4a5b-4c6d-9e8f-0a1b2c3d4e5f"
)

func savedDB() *fakeDB {
	return &fakeDB{Users: map[string]map[string]any{"k1": user(1, "alice")}}
}

func postStatus(t *testing.T, r *core.Registry, key string, pv map[string]string, ck string, wantJSON bool) (status int, loc, body string) {
	t.Helper()
	resp := post(t, r, key, pv, url.Values{}, ck, wantJSON)
	return resp.Status, location(resp), resp.Body
}

func TestFavoritesStoryIsIdempotent(t *testing.T) {
	db := savedDB()
	install(t, db)
	r := newRegistry(t, nil)
	for i := 0; i < 2; i++ {
		resp := post(t, r, "POST /favorite/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, url.Values{"next": {"/news?p=2"}}, "k1", false)
		if resp.Status != 303 || location(resp) != "/news?p=2" {
			t.Fatalf("favorite %d: %d %q", i, resp.Status, location(resp))
		}
	}
	if len(db.Execs) != 2 {
		t.Fatalf("execs %v", db.Execs)
	}
	for _, e := range db.Execs {
		if e.SQL != sqlFavStory || len(e.Args) != 2 || e.Args[0] != float64(1) || e.Args[1] != float64(42) {
			t.Errorf("exec %+v", e)
		}
	}
	db.Execs = nil
	post(t, r, "POST /unfavorite/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, url.Values{}, "k1", false)
	if len(db.Execs) != 1 || db.Execs[0].SQL != sqlUnfavStory {
		t.Errorf("unfavorite execs %v", db.Execs)
	}
}

func TestFavoritesZeroRowsIsNotAnError(t *testing.T) {
	db := savedDB()
	db.Exec = func(string, []any) (int64, error) { return 0, nil }
	install(t, db)
	r := newRegistry(t, nil)
	resp := post(t, r, "POST /favorite/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, url.Values{}, "k1", true)
	if resp.Status != 200 || !strings.Contains(resp.Body, `"favorited":true`) {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
}

func TestFavoritesComment(t *testing.T) {
	db := savedDB()
	install(t, db)
	r := newRegistry(t, nil)
	post(t, r, "POST /favorite/{kind}/{id}", map[string]string{"kind": "comment", "id": commentUUID}, url.Values{}, "k1", false)
	post(t, r, "POST /unfavorite/{kind}/{id}", map[string]string{"kind": "comment", "id": commentUUID}, url.Values{}, "k1", false)
	if len(db.Execs) != 2 || db.Execs[0].SQL != sqlFavComment || db.Execs[0].Args[1] != commentUUID || db.Execs[1].SQL != sqlUnfavComment {
		t.Fatalf("execs %v", db.Execs)
	}
	db.Execs = nil
	for _, pv := range []map[string]string{
		{"kind": "comment", "id": "not-a-uuid"}, {"kind": "comment", "id": "42"}, {"kind": "user", "id": "42"}, {"kind": "story", "id": "abc"}, {"kind": "story", "id": storyUUID},
	} {
		resp := post(t, r, "POST /favorite/{kind}/{id}", pv, url.Values{}, "k1", false)
		if resp.Status != 303 || !strings.Contains(location(resp), "m=saved-missing") {
			t.Errorf("%v: %d %q", pv, resp.Status, location(resp))
		}
		if j := post(t, r, "POST /favorite/{kind}/{id}", pv, url.Values{}, "k1", true); j.Status != 404 {
			t.Errorf("%v: json status %d", pv, j.Status)
		}
	}
	if len(db.Execs) != 0 {
		t.Errorf("refusals ran %v", db.Execs)
	}
}

func TestFavoritesSignedOut(t *testing.T) {
	db := savedDB()
	install(t, db)
	r := newRegistry(t, nil)
	for _, ck := range []string{"", "unknown"} {
		status, loc, _ := postStatus(t, r, "POST /favorite/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, ck, false)
		if status != 303 || loc != "/login?m=saved-login" {
			t.Errorf("%q: %d %q", ck, status, loc)
		}
		if status, _, _ = postStatus(t, r, "POST /hide/{id}", map[string]string{"id": "42"}, ck, true); status != 401 {
			t.Errorf("%q: json status %d", ck, status)
		}
	}
	if len(db.Execs) != 0 {
		t.Errorf("execs %v", db.Execs)
	}
}

func TestFavoritesHostError(t *testing.T) {
	db := savedDB()
	db.Exec = func(string, []any) (int64, error) { return 0, hostErr("db_error") }
	install(t, db)
	r := newRegistry(t, nil)
	resp := post(t, r, "POST /favorite/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, url.Values{"next": {"/news"}}, "k1", false)
	if resp.Status != 303 || location(resp) != "/news?m=saved-error" {
		t.Errorf("%d %q", resp.Status, location(resp))
	}
	resp = post(t, r, "POST /hide/{id}", map[string]string{"id": "42"}, url.Values{}, "k1", false)
	if resp.Status != 303 || !strings.Contains(location(resp), "m=saved-error") {
		t.Errorf("hide: %d %q", resp.Status, location(resp))
	}
}

func TestHideRoutes(t *testing.T) {
	db := savedDB()
	install(t, db)
	r := newRegistry(t, nil)
	resp := post(t, r, "POST /hide/{id}", map[string]string{"id": "42"}, url.Values{}, "k1", true)
	if resp.Status != 200 || !strings.Contains(resp.Body, `"hidden":true`) {
		t.Errorf("%d %s", resp.Status, resp.Body)
	}
	post(t, r, "POST /unhide/{id}", map[string]string{"id": "42"}, url.Values{}, "k1", false)
	if len(db.Execs) != 2 || db.Execs[0].SQL != sqlHide || db.Execs[1].SQL != sqlUnhide || db.Execs[1].Args[1] != float64(42) {
		t.Fatalf("execs %v", db.Execs)
	}
	db.Execs = nil
	resp = post(t, r, "POST /hide/{id}", map[string]string{"id": "abc"}, url.Values{}, "k1", false)
	if resp.Status != 303 || !strings.Contains(location(resp), "m=saved-missing") || len(db.Execs) != 0 {
		t.Errorf("bad id: %d %q %v", resp.Status, location(resp), db.Execs)
	}
}

func TestHideRowActions(t *testing.T) {
	db := savedDB()
	state := `{"fav":false,"hid":false}`
	db.Query = func(sql string, _ []any) (any, error) {
		if sql == sqlState {
			return map[string]any{"rows": [][]any{{state}}}, nil
		}
		return map[string]any{"rows": [][]any{}}, nil
	}
	h := install(t, db)
	r := newRegistry(t, nil)
	story := core.Item{Kind: "story", ID: storyUUID, ItemNo: 42, AuthorID: 2}
	alice := &core.User{ID: 1, Username: "alice"}
	labels := func(acts []core.Action) []string {
		var out []string
		for _, a := range acts {
			out = append(out, a.Label)
		}
		return out
	}

	if got := r.ActionsFor(nil, story); len(got) != 0 || len(h.Calls) != 0 {
		t.Errorf("signed out: %v, calls %v", got, h.Calls)
	}
	if got := r.ActionsFor(&core.User{ID: 1}, story); len(got) != 0 || len(h.Calls) != 0 {
		t.Errorf("no username: %v, calls %v", got, h.Calls)
	}
	if got := r.ActionsFor(alice, core.Item{Kind: "comment", ID: commentUUID, ItemNo: 42}); len(got) != 0 || len(h.Calls) != 0 {
		t.Errorf("comment: %v, calls %v", got, h.Calls)
	}

	got := r.ActionsFor(alice, story)
	if !slices.Equal(labels(got), []string{"hide", "favorite"}) ||
		got[0] != (core.Action{Label: "hide", Href: "/p/hn/hide/42", Class: "hn-hide", Post: true}) ||
		got[1] != (core.Action{Label: "favorite", Href: "/p/hn/favorite/story/42", Class: "hn-fav", Post: true}) {
		t.Errorf("fresh: %+v", got)
	}
	state = `{"fav":true,"hid":true}`
	got = r.ActionsFor(alice, story)
	if !slices.Equal(labels(got), []string{"unhide", "unfavorite"}) ||
		got[0] != (core.Action{Label: "unhide", Href: "/p/hn/unhide/42", Class: "hn-unhide", Post: true}) ||
		got[1] != (core.Action{Label: "unfavorite", Href: "/p/hn/unfavorite/story/42", Class: "hn-unfav", Post: true}) {
		t.Errorf("set: %+v", got)
	}
	if q := db.Queries[len(db.Queries)-1]; len(q.Args) != 2 || q.Args[0] != float64(1) || q.Args[1] != storyUUID {
		t.Errorf("state args %v", q.Args)
	}

	db.Query = func(string, []any) (any, error) { return nil, hostErr("db_error") }
	if got := r.ActionsFor(alice, story); len(got) != 0 {
		t.Errorf("host error: %v", got)
	}
}
