package moderation

import (
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestRowActions(t *testing.T) {
	story := core.Item{Kind: "story", ID: storyUUID, ItemNo: 42, AuthorID: 2}
	dead := story
	dead.Dead = true
	comment := core.Item{Kind: "comment", ID: commentID, ItemNo: 42, AuthorID: 2}
	viewer := func(karma int) *core.User { return &core.User{ID: 1, Karma: karma} }

	t.Run("no viewer", func(t *testing.T) {
		install(t, &fakeDB{})
		if a := rowActions(nil, story); a != nil {
			t.Fatalf("actions = %+v", a)
		}
	})
	t.Run("low karma", func(t *testing.T) {
		install(t, &fakeDB{})
		if a := rowActions(viewer(29), story); a != nil {
			t.Fatalf("actions = %+v", a)
		}
	})
	t.Run("flag", func(t *testing.T) {
		install(t, &fakeDB{})
		a := rowActions(viewer(30), story)
		if len(a) != 1 || a[0].Label != "flag" || a[0].Class != "hn-flag" || !a[0].Post || a[0].Href != "/p/hn/flag/story/42?next=%2Fitem%2F42" {
			t.Fatalf("actions = %+v", a)
		}
		a = rowActions(viewer(30), comment)
		if len(a) != 1 || a[0].Href != "/p/hn/flag/comment/"+commentID+"?next=%2Fitem%2F42" {
			t.Fatalf("comment actions = %+v", a)
		}
	})
	t.Run("vouch", func(t *testing.T) {
		install(t, &fakeDB{})
		if a := rowActions(viewer(30), dead); a != nil {
			t.Fatalf("karma 30 got %+v", a)
		}
		a := rowActions(viewer(31), dead)
		if len(a) != 1 || a[0].Label != "vouch" || a[0].Class != "hn-vouch" || a[0].Href != "/p/hn/vouch/story/42?next=%2Fitem%2F42" {
			t.Fatalf("actions = %+v", a)
		}
	})
	t.Run("own item", func(t *testing.T) {
		install(t, &fakeDB{})
		own := story
		own.AuthorID = 1
		if a := rowActions(viewer(99), own); a != nil {
			t.Fatalf("actions = %+v", a)
		}
	})
	t.Run("twenty rows cost one settings read and no query", func(t *testing.T) {
		db := &fakeDB{}
		h := install(t, db)
		v := viewer(50)
		for i := 0; i < 20; i++ {
			if a := rowActions(v, story); len(a) != 1 {
				t.Fatalf("row %d: %+v", i, a)
			}
		}
		n := 0
		for _, c := range h.Calls {
			if c == "settings.get" {
				n++
			}
		}
		if n > 1 || len(db.Queries) != 0 {
			t.Fatalf("settings.get x%d, queries %+v", n, db.Queries)
		}
	})
}
