package moderation

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func vouchPost(t *testing.T, kind, id, key string) (resp sdk.HTTPResponse) {
	return postTo(t, "/vouch/{kind}/{id}", map[string]string{"kind": kind, "id": id}, url.Values{"next": {"/news"}}, nil, key, false)
}

func TestVouch(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))

	refusals := []struct {
		name  string
		karma int
		item  map[string]any
		want  string
	}{
		{"alive", 50, item(storyUUID, 42, 2, false, ""), "mod-vouch-alive"},
		{"owner kill", 50, item(storyUUID, 42, 2, true, "owner"), "mod-vouch-locked"},
		{"ban kill", 50, item(storyUUID, 42, 2, true, "ban"), "mod-vouch-locked"},
		{"domain kill", 50, item(storyUUID, 42, 2, true, "domain"), "mod-vouch-locked"},
		{"low karma", 30, item(storyUUID, 42, 2, true, "flags"), "mod-vouch-karma"},
		{"own item", 50, item(storyUUID, 42, 1, true, "flags"), "mod-vouch-own"},
		{"missing", 50, nil, "mod-missing"},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			db := modDB(c.karma, c.item, 0)
			h := install(t, db)
			resp := vouchPost(t, "story", "42", "k1")
			if resp.Status != 303 || loc(resp) != "/news?m="+c.want {
				t.Fatalf("resp %d %q", resp.Status, loc(resp))
			}
			if len(db.Execs) != 0 {
				t.Fatalf("execs = %+v", db.Execs)
			}
			noEntityCalls(t, h)
		})
	}

	t.Run("signed out", func(t *testing.T) {
		install(t, modDB(50, item(storyUUID, 42, 2, true, "flags"), 0))
		if resp := vouchPost(t, "story", "42", ""); loc(resp) != "/login?m=mod-login" {
			t.Fatalf("location %q", loc(resp))
		}
	})

	t.Run("story brought back", func(t *testing.T) {
		db := modDB(31, item(storyUUID, 42, 2, true, "flags"), 0)
		h := install(t, db)
		resp := vouchPost(t, "story", "42", "k1")
		if loc(resp) != "/news?m=mod-vouched" {
			t.Fatalf("location %q", loc(resp))
		}
		if v := execs(db, "INSERT INTO vouches"); len(v) != 1 || v[0].Args[2] != float64(1) {
			t.Fatalf("vouches = %+v", v)
		}
		if d := execs(db, "UPDATE story_meta SET dead"); len(d) != 1 || d[0].Args[1] != false {
			t.Fatalf("dead update = %+v", d)
		}
		if len(execs(db, "DELETE FROM flags")) != 1 || len(execs(db, "DELETE FROM kills")) != 1 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		var actions []string
		for _, l := range execs(db, sqlLogInsert) {
			if l.Args[0] != "alice" || l.Args[2] != "story:42" {
				t.Errorf("log %+v", l)
			}
			actions = append(actions, l.Args[1].(string))
		}
		if strings.Join(actions, ",") != "restore,vouch" {
			t.Errorf("log actions %v", actions)
		}
		noEntityCalls(t, h)
	})

	t.Run("comment the host did not change stays out of search", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, true, "flags"), 0)
		db.Comments = func(fn string, ids []string) (any, error) { return map[string]any{"changed": 0}, nil }
		install(t, db)
		vouchPost(t, "comment", commentID, "k1")
		if k := execs(db, "DELETE FROM kills"); len(k) != 1 || k[0].Args[2] != float64(0) {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})

	t.Run("comment brought back", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, true, "flags"), 0)
		var restored []string
		db.Comments = func(fn string, ids []string) (any, error) {
			restored = append(restored, fn+":"+strings.Join(ids, ","))
			return map[string]any{"changed": 1}, nil
		}
		h := install(t, db)
		resp := vouchPost(t, "comment", commentID, "k1")
		if loc(resp) != "/news?m=mod-vouched" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(restored) != 1 || restored[0] != sdk.FnCommentsRestore+":"+commentID {
			t.Fatalf("restored = %v", restored)
		}
		if k := execs(db, "DELETE FROM kills"); len(k) != 1 || !strings.Contains(k[0].SQL, "SET live = true") || k[0].Args[2] != float64(1) {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})

	t.Run("restore unavailable keeps the kill", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, true, "flags"), 0)
		db.Comments = func(string, []string) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }
		h := install(t, db)
		resp := vouchPost(t, "comment", commentID, "k1")
		if loc(resp) != "/news?m=mod-off" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(execs(db, "DELETE")) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})
}
