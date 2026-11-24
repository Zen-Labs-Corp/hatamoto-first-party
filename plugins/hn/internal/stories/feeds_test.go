package stories

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

var feedNow = mustTime("2026-11-25T12:00:00Z")

func qf(kv ...string) func(string) string {
	m := map[string]string{}
	for i := 0; i+1 < len(kv); i += 2 {
		m[kv[i]] = kv[i+1]
	}
	return func(k string) string { return m[k] }
}

func TestFeedsBuild(t *testing.T) {
	pinNow(t, feedNow)
	cases := []struct {
		path  string
		query []string
		where []string
		order string
		extra []any
	}{
		{"/", nil, []string{"NOT s.dead AND s.kind <> 'job'"}, scoreOrder, nil},
		{"/news", nil, []string{"NOT s.dead AND s.kind <> 'job'"}, scoreOrder, nil},
		{"/newest", nil, []string{"s.kind <> 'job'"}, "s.item_no DESC", nil},
		{"/newest", []string{"id", "bob"}, []string{"u.username_lower = lower($5)"}, "s.item_no DESC", []any{"bob"}},
		{"/newest", []string{"next", "500"}, []string{"s.item_no < $5"}, "s.item_no DESC", []any{int64(500)}},
		{"/front", []string{"day", "2026-11-20"}, []string{"s.created_at >= $5::timestamptz", "+ interval '1 day'"}, pointOrder, []any{"2026-11-20T00:00:00Z"}},
		{"/front", nil, []string{"s.created_at >= $5::timestamptz"}, pointOrder, []any{"2026-11-24T00:00:00Z"}},
		{"/ask", nil, []string{"s.kind = 'ask'"}, scoreOrder, nil},
		{"/show", nil, []string{"s.kind = 'show'"}, scoreOrder, nil},
		{"/jobs", nil, []string{"s.kind = 'job'"}, dateOrder, nil},
		{"/best", nil, []string{"s.created_at > $5::timestamptz - interval '7 days'"}, pointOrder, []any{"2026-11-25T12:00:00Z"}},
		{"/from", []string{"site", "www.Example.com"}, []string{"s.domain = $5"}, dateOrder, []any{"example.com"}},
	}
	for _, c := range cases {
		q, ok := buildFeed(feedReq{Path: c.path, Q: qf(c.query...), Viewer: &core.User{ID: 7}, PageSize: 30})
		if !ok {
			t.Fatalf("%s %v: not a feed", c.path, c.query)
		}
		for _, w := range c.where {
			if !strings.Contains(q.SQL, w) {
				t.Errorf("%s %v: SQL lacks %q", c.path, c.query, w)
			}
		}
		if !strings.Contains(q.SQL, "ORDER BY "+c.order+" LIMIT $3 OFFSET $4") {
			t.Errorf("%s %v: order is not %q: %s", c.path, c.query, c.order, q.SQL)
		}
		want := append([]any{int64(7), false, 31, 0}, c.extra...)
		if len(q.Args) != len(want) {
			t.Errorf("%s %v: args %v, want %v", c.path, c.query, q.Args, want)
			continue
		}
		for i := range want {
			if q.Args[i] != want[i] && !(i == 0 || i == 2 || i == 3) { // viewer id, limit, offset are checked as numbers below
				t.Errorf("%s %v: arg %d = %v, want %v", c.path, c.query, i+1, q.Args[i], want[i])
			}
		}
		if q.Args[0] != int64(7) || q.Args[2] != 31 || q.Args[3] != 0 || q.FirstRank != 1 {
			t.Errorf("%s %v: head args %v rank %d", c.path, c.query, q.Args[:4], q.FirstRank)
		}
		if !strings.Contains(q.SQL, "NOT EXISTS (SELECT 1 FROM hidden h WHERE h.story_id = s.entity_id AND h.user_id = $1)") {
			t.Errorf("%s: hidden stories are not filtered", c.path)
		}
	}
	if _, ok := buildFeed(feedReq{Path: "/nope", Q: qf(), PageSize: 30}); ok {
		t.Error("/nope is a feed")
	}
}

func TestFeedsShowdead(t *testing.T) {
	sd := &core.User{ID: 1, Showdead: true}
	if q, _ := buildFeed(feedReq{Path: "/news", Q: qf(), Viewer: sd, PageSize: 30}); q.Args[1] != false {
		t.Errorf("news shows dead: %v", q.Args[1])
	}
	if q, _ := buildFeed(feedReq{Path: "/ask", Q: qf(), Viewer: sd, PageSize: 30}); q.Args[1] != true {
		t.Errorf("ask hides dead from a showdead viewer: %v", q.Args[1])
	}
	q, _ := buildFeed(feedReq{Path: "/ask", Q: qf(), PageSize: 30})
	if q.Args[0] != int64(0) || q.Args[1] != false {
		t.Errorf("signed out args %v", q.Args[:2])
	}
}

func rowsJSON(n int, firstNo int64) string {
	var rows []map[string]any
	for i := 0; i < n; i++ {
		rows = append(rows, map[string]any{"entity_id": entityID, "item_no": firstNo - int64(i), "author_id": 2, "by": "bob", "title": "T", "kind": "story",
			"created_at": "2026-11-25T11:00:00Z"})
	}
	b, _ := json.Marshal(rows)
	return string(b)
}

func TestFeedsPaging(t *testing.T) {
	pinNow(t, feedNow)
	var gotArgs []any
	db := &fakeDB{Query: func(_ string, args []any) (any, error) {
		gotArgs = args
		return map[string]any{"rows": [][]any{{rowsJSON(31, 900)}}}, nil
	}}
	install(t, db)
	rows, more, first, _, err := loadFeed(feedReq{Path: "/news", Q: qf("p", "2"), PageSize: 30})
	if err != nil || len(rows) != 30 || more != "/news?p=3" || first != 31 || gotArgs[3] != float64(30) {
		t.Fatalf("p=2: %d rows more %q first %d args %v err %v", len(rows), more, first, gotArgs, err)
	}
	_, more, _, _, _ = loadFeed(feedReq{Path: "/newest", Q: qf(), PageSize: 30})
	if more != "/newest?next=871" {
		t.Errorf("newest more %q", more)
	}
	_, more, _, _, _ = loadFeed(feedReq{Path: "/newest", Q: qf("id", "bob"), PageSize: 30})
	if more != "/newest?id=bob&next=871" {
		t.Errorf("newest by id more %q", more)
	}
	db.Query = func(string, []any) (any, error) { return map[string]any{"rows": [][]any{{rowsJSON(30, 900)}}}, nil }
	if _, more, _, _, _ = loadFeed(feedReq{Path: "/news", Q: qf(), PageSize: 30}); more != "" {
		t.Errorf("30 rows give more %q", more)
	}
}

func TestFeedsFromEmptySite(t *testing.T) {
	db := &fakeDB{}
	install(t, db)
	rows, more, _, _, err := loadFeed(feedReq{Path: "/from", Q: qf("site", " "), PageSize: 30})
	if err != nil || len(rows) != 0 || more != "" || len(db.Queries) != 0 {
		t.Errorf("rows %v more %q queries %d err %v", rows, more, len(db.Queries), err)
	}
}

func TestFeedsRowView(t *testing.T) {
	pinNow(t, feedNow)
	install(t, &fakeDB{})
	r := newRegistry(t, map[string]func(*core.Registry){"parity": func(r *core.Registry) {
		r.RowActions(func(*core.User, core.Item) []core.Action {
			return []core.Action{{Label: "flag", Href: "/p/hn/flag/story/42", Class: "hn-flag", Post: true}}
		})
	}})
	st := core.DefaultSettings()
	created := mustTime("2026-11-25T11:30:00Z")
	base := storyRow{EntityID: entityID, ItemNo: 42, AuthorID: 2, By: "bob", Title: "T", URL: "https://example.com/a", Domain: "example.com", Kind: "story", Points: 3, CreatedAt: created}
	alice := &core.User{ID: 1, Username: "alice"}
	labels := func(m map[string]any) (out []string) {
		for _, a := range m["actions"].([]map[string]any) {
			out = append(out, a["label"].(string))
		}
		return
	}

	m := rowView(r, nil, base, 3, "/news", st)
	if m["vote"] != "up" || m["rank"] != "3." || m["score"] != "3 points" || m["comments"] != "discuss" || m["href"] != "https://example.com/a" || m["external"] != true {
		t.Errorf("signed out view %v", m)
	}
	if m["vote_href"] != "/p/hn/vote/story/42" || m["item_href"] != "/item/42" || m["site_href"] != "/from?site=example.com" || m["age"] != "30 minutes ago" {
		t.Errorf("hrefs %v", m)
	}
	if got := labels(m); len(got) != 1 || got[0] != "flag" {
		t.Errorf("signed out actions %v", got)
	}

	job := base
	job.Kind, job.URL = "job", ""
	m = rowView(r, alice, job, 1, "/jobs", st)
	if m["vote"] != "none" || m["score"] != "" || m["comments"] != "" || m["href"] != "/item/42" || m["external"] != false {
		t.Errorf("job view %v", m)
	}

	own := base
	own.AuthorID = 1
	m = rowView(r, alice, own, 1, "/news", st)
	if m["vote"] != "none" {
		t.Errorf("own vote %v", m["vote"])
	}
	if got := labels(m); strings.Join(got, ",") != "edit,delete,flag" {
		t.Errorf("own actions %v", got)
	}

	voted := base
	at := feedNow.Add(-10 * time.Minute)
	voted.VotedAt = &at
	m = rowView(r, alice, voted, 1, "/news", st)
	if got := labels(m); m["vote"] != "voted" || strings.Join(got, ",") != "unvote,flag" {
		t.Errorf("voted view %v %v", m["vote"], got)
	}
	if a := m["actions"].([]map[string]any)[0]; a["href"] != "/p/hn/vote/story/42?how=un" || a["post"] != true {
		t.Errorf("unvote action %v", a)
	}
	late := feedNow.Add(-2 * time.Hour)
	voted.VotedAt = &late
	if got := labels(rowView(r, alice, voted, 1, "/news", st)); strings.Join(got, ",") != "flag" {
		t.Errorf("late vote actions %v", got)
	}

	dead := base
	dead.Dead = true
	if m = rowView(r, alice, dead, 0, "/item/42", st); m["vote"] != "none" || m["title"] != "[dead] T" || m["rank"] != "" {
		t.Errorf("dead view %v", m)
	}
}
