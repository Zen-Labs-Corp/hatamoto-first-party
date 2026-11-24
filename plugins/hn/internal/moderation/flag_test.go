package moderation

import (
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const storyUUID = "22222222-2222-2222-2222-222222222222"

// item is the answer of a resolve query.
func item(id string, no, author int64, dead bool, cause string) map[string]any {
	return map[string]any{"id": id, "item_no": no, "author_id": author, "dead": dead, "cause": cause}
}

// withKarma is a users row with that karma.
func withKarma(id int64, name string, karma int) map[string]any {
	u := user(id, name)
	u["karma"] = karma
	return u
}

// modDB has member k1 (id 1) and the item the resolve queries answer with.
// flagCount answers the flag count query.
func modDB(karma int, it map[string]any, flagCount int) *fakeDB {
	return &fakeDB{
		Users: map[string]map[string]any{"k1": withKarma(1, "alice", karma)},
		Query: func(sql string, _ []any) (any, error) {
			switch sql {
			case sqlResolveStory, sqlResolveStoryID, sqlResolveComment:
				if it == nil {
					return map[string]any{"rows": [][]any{}}, nil
				}
				return jsonRow(it), nil
			case sqlFlagCount:
				return jsonRow(map[string]any{"n": flagCount}), nil
			case sqlBlocked:
				return jsonRow(map[string]any{"blocked": false}), nil
			}
			return nil, hostErr(sdk.ErrInvalidArgument)
		},
	}
}

func postTo(t *testing.T, pattern string, vals map[string]string, form url.Values, query map[string][]string, key string, wantJSON bool) sdk.HTTPResponse {
	t.Helper()
	headers := map[string]string{"sec-fetch-site": "same-origin"}
	if wantJSON {
		headers["accept"] = "application/json"
	}
	resp, err := newRegistry(t).ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: pattern, PathValues: vals, Headers: headers, Query: query, Body: form.Encode(), CommenterKey: key})
	if err != nil {
		t.Fatalf("%s: %v", pattern, err)
	}
	return resp
}

func flagPost(t *testing.T, kind, id string, form url.Values, key string, wantJSON bool) sdk.HTTPResponse {
	return postTo(t, "/flag/{kind}/{id}", map[string]string{"kind": kind, "id": id}, form, nil, key, wantJSON)
}

func loc(resp sdk.HTTPResponse) string { return resp.Headers["Location"] }

func execs(db *fakeDB, part string) []sqlCall {
	var out []sqlCall
	for _, c := range db.Execs {
		if strings.Contains(c.SQL, part) {
			out = append(out, c)
		}
	}
	return out
}

func TestFlag(t *testing.T) {
	pinNow(t, time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC))
	form := url.Values{"next": {"/news"}}

	refusals := []struct {
		name   string
		karma  int
		item   map[string]any
		key    string
		want   string
		exists bool
	}{
		{"signed out", 50, item(storyUUID, 42, 2, false, ""), "", "/login?m=mod-login", true},
		{"low karma", 29, item(storyUUID, 42, 2, false, ""), "k1", "/news?m=mod-flag-karma", true},
		{"own item", 50, item(storyUUID, 42, 1, false, ""), "k1", "/news?m=mod-flag-own", true},
		{"dead item", 50, item(storyUUID, 42, 2, true, "flags"), "k1", "/news?m=mod-flag-dead", true},
		{"missing item", 50, nil, "k1", "/news?m=mod-missing", true},
	}
	for _, c := range refusals {
		t.Run(c.name, func(t *testing.T) {
			db := modDB(c.karma, c.item, 1)
			h := install(t, db)
			resp := flagPost(t, "story", "42", form, c.key, false)
			if resp.Status != 303 || loc(resp) != c.want {
				t.Fatalf("resp %d %q, want %q", resp.Status, loc(resp), c.want)
			}
			if len(db.Execs) != 0 {
				t.Fatalf("execs = %+v", db.Execs)
			}
			noEntityCalls(t, h)
		})
	}

	t.Run("repeat", func(t *testing.T) {
		db := modDB(50, item(storyUUID, 42, 2, false, ""), 1)
		db.Exec = func(sql string, _ []any) (int64, error) {
			if sql == sqlFlagInsert {
				return 0, nil
			}
			return 1, nil
		}
		install(t, db)
		if resp := flagPost(t, "story", "42", form, "k1", false); loc(resp) != "/news?m=mod-flag-again" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(db.Execs) != 1 {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})

	t.Run("below the threshold", func(t *testing.T) {
		db := modDB(50, item(storyUUID, 42, 2, false, ""), 4)
		h := install(t, db)
		resp := flagPost(t, "story", "42", form, "k1", false)
		if loc(resp) != "/news?m=mod-flagged" || len(db.Execs) != 1 {
			t.Fatalf("location %q execs %+v", loc(resp), db.Execs)
		}
		noEntityCalls(t, h)
	})

	t.Run("fifth story flag kills it", func(t *testing.T) {
		db := modDB(50, item(storyUUID, 42, 2, false, ""), 5)
		h := install(t, db)
		resp := flagPost(t, "story", "42", nil, "k1", true)
		if resp.Status != 200 || resp.Body != `{"dead":true,"flagged":true}` {
			t.Fatalf("resp %d %s", resp.Status, resp.Body)
		}
		if d := execs(db, "UPDATE story_meta SET dead"); len(d) != 1 || d[0].Args[0] != storyUUID || d[0].Args[1] != true {
			t.Fatalf("dead update = %+v", d)
		}
		if k := execs(db, "INSERT INTO kills"); len(k) != 1 || k[0].Args[0] != "story" || k[0].Args[2] != "flags" {
			t.Fatalf("kills = %+v", k)
		}
		noEntityCalls(t, h)
	})

	t.Run("flags_to_dead setting", func(t *testing.T) {
		db := modDB(50, item(storyUUID, 42, 2, false, ""), 2)
		db.Settings = map[string]any{"flags_to_dead": 2}
		h := install(t, db)
		flagPost(t, "story", "42", nil, "k1", false)
		if len(execs(db, "UPDATE story_meta SET dead")) != 1 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		noEntityCalls(t, h)
	})

	t.Run("third comment flag marks it dead", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, false, ""), 3)
		var marked []string
		db.Comments = func(fn string, ids []string) (any, error) {
			marked = append(marked, fn+":"+strings.Join(ids, ","))
			return map[string]any{"changed": 1}, nil
		}
		h := install(t, db)
		resp := flagPost(t, "comment", commentID, form, "k1", false)
		if loc(resp) != "/news?m=mod-flagged" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(marked) != 1 || marked[0] != sdk.FnCommentsMarkDead+":"+commentID {
			t.Fatalf("marked = %v", marked)
		}
		if k := execs(db, "INSERT INTO kills"); len(k) != 1 || k[0].Args[0] != "comment" || k[0].Args[2] != "flags" || !strings.Contains(k[0].SQL, "SET live = false") {
			t.Fatalf("kills = %+v", k)
		}
		noEntityCalls(t, h)
	})

	t.Run("mark_dead unavailable keeps the flag", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, false, ""), 3)
		db.Comments = func(string, []string) (any, error) { return nil, hostErr(sdk.ErrUnavailable) }
		h := install(t, db)
		resp := flagPost(t, "comment", commentID, form, "k1", false)
		if loc(resp) != "/news?m=mod-flagged" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(execs(db, "INSERT INTO kills")) != 0 || len(execs(db, "INSERT INTO flags")) != 1 || len(execs(db, "DELETE")) != 0 {
			t.Fatalf("execs = %+v", db.Execs)
		}
		logs := execs(db, sqlLogInsert)
		if len(logs) != 1 || logs[0].Args[1] != "kill-unavailable" || logs[0].Args[2] != "comment:"+commentID {
			t.Fatalf("log = %+v", logs)
		}
		noEntityCalls(t, h)
	})

	t.Run("mark_dead moving nothing keeps the flag and records no kill", func(t *testing.T) {
		db := modDB(50, item(commentID, 7, 2, false, ""), 3)
		db.Comments = func(string, []string) (any, error) { return map[string]any{"changed": 0}, nil }
		install(t, db)
		resp := flagPost(t, "comment", commentID, form, "k1", false)
		if loc(resp) != "/news?m=mod-flagged" {
			t.Fatalf("location %q", loc(resp))
		}
		if len(execs(db, "INSERT INTO kills")) != 0 || len(execs(db, "comment_meta")) != 0 || len(execs(db, "INSERT INTO flags")) != 1 {
			t.Fatalf("execs = %+v", db.Execs)
		}
	})

	t.Run("next from the query and unsafe next", func(t *testing.T) {
		install(t, modDB(50, item(storyUUID, 42, 2, false, ""), 1))
		resp := postTo(t, "/flag/{kind}/{id}", map[string]string{"kind": "story", "id": "42"}, nil, map[string][]string{"next": {"/item/42"}}, "k1", false)
		if loc(resp) != "/item/42?m=mod-flagged" {
			t.Fatalf("query next: %q", loc(resp))
		}
		resp = flagPost(t, "story", "42", url.Values{"next": {"//evil.example"}}, "k1", false)
		if loc(resp) != "/?m=mod-flagged" {
			t.Fatalf("unsafe next: %q", loc(resp))
		}
	})

	t.Run("json refusal", func(t *testing.T) {
		install(t, modDB(29, item(storyUUID, 42, 2, false, ""), 1))
		resp := flagPost(t, "story", "42", nil, "k1", true)
		if resp.Status != 403 || resp.Body != `{"error":"`+core.NoticeText("mod-flag-karma")+`"}` {
			t.Fatalf("resp %d %s", resp.Status, resp.Body)
		}
	})

	t.Run("bad ids reach no sql", func(t *testing.T) {
		db := modDB(50, item(storyUUID, 42, 2, false, ""), 1)
		install(t, db)
		for _, c := range [][2]string{{"comment", "not-a-uuid"}, {"story", "x1"}, {"story", "0"}, {"thing", "42"}, {"comment", strings.ToUpper(commentID) + "'"}} {
			resp := flagPost(t, c[0], c[1], form, "k1", false)
			if loc(resp) != "/news?m=mod-missing" {
				t.Errorf("%v: %q", c, loc(resp))
			}
		}
		if len(db.Queries) != 0 || len(db.Execs) != 0 {
			t.Fatalf("sql ran: %+v %+v", db.Queries, db.Execs)
		}
	})

	t.Run("oversized body", func(t *testing.T) {
		install(t, modDB(50, item(storyUUID, 42, 2, false, ""), 1))
		resp := flagPost(t, "story", "42", url.Values{"x": {strings.Repeat("a", 70<<10)}}, "k1", true)
		if resp.Status != 413 {
			t.Fatalf("status %d", resp.Status)
		}
	})
}
