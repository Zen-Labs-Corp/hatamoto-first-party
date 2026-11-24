package comments

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

var epoch = time.Date(2026, 5, 1, 12, 0, 0, 0, time.UTC)

func renderPage(t *testing.T, ctx sdk.PageContext) sdk.PageContext {
	t.Helper()
	out, err := newRegistry(nil).RenderPage(ctx)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

func threadsBlock(t *testing.T, ctx sdk.PageContext) map[string]any {
	t.Helper()
	for _, b := range ctx.Blocks {
		if b.Type == "hn_threads" {
			raw, _ := json.Marshal(b.Content)
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			return c
		}
	}
	t.Fatalf("no hn_threads block in %+v", ctx.Blocks)
	return nil
}

func rowsOf(c map[string]any) []map[string]any {
	var out []map[string]any
	for _, r := range c["rows"].([]any) {
		out = append(out, r.(map[string]any))
	}
	return out
}

// cid makes the i-th test comment id.
func cid(i int) string { return fmt.Sprintf("00000000-0000-4000-8000-%012d", i) }

// newcommentsFake has story 7 (live) and story 8 (dead), viewer k1 "ann", author k2 "bob".
func newcommentsFake(t *testing.T) (*fake, *core.User, *core.User) {
	f := installFake(t)
	ann, bob := f.user("k1", "ann", 10), f.user("k2", "bob", 1)
	f.stories[eid1] = &storyRow{ItemNo: 7, Title: "Seven"}
	f.stories[eid2] = &storyRow{ItemNo: 8, Title: "Eight", Dead: true}
	core.Now = func() time.Time { return epoch.Add(time.Hour) }
	t.Cleanup(func() { core.Now = func() time.Time { return time.Now().UTC() } })
	return f, ann, bob
}

func (f *fake) comment(i int, author *core.User, minute int) *metaRow {
	m := &metaRow{ID: cid(i), Entity: eid1, Name: "bob", Body: fmt.Sprintf("<p>text %d</p>", i), Live: true, Points: 1,
		Created: epoch.Add(time.Duration(minute) * time.Minute)}
	if author != nil {
		m.Author, m.Name = &author.ID, author.Username
	}
	f.meta[m.ID] = m
	return m
}

func newcomments(t *testing.T, key string, q map[string]string) map[string]any {
	t.Helper()
	ctx := sdk.PageContext{Path: "/newcomments", CommenterKey: key, Query: q}
	return threadsBlock(t, renderPage(t, ctx))
}

func TestThreadsPageNewcomments(t *testing.T) {
	t.Run("pages of 30 newest first", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		for i := 1; i <= 35; i++ {
			f.comment(i, bob, i)
		}
		c := newcomments(t, "", nil)
		rows := rowsOf(c)
		if len(rows) != 30 || rows[0]["id"] != cid(35) || rows[29]["id"] != cid(6) {
			t.Fatalf("got %d rows, first %v last %v", len(rows), rows[0]["id"], rows[len(rows)-1]["id"])
		}
		if c["more_href"] != "/newcomments?p=2" || c["mode"] != "newcomments" || c["empty"] != false {
			t.Errorf("content: %+v", c)
		}
		r := rows[0]
		if r["item_href"] != "/item/7" || r["title"] != "Seven" || r["context_href"] != "/item/7?id="+cid(35) ||
			r["author_href"] != "/user?id=bob" || r["ago"] != "25 minutes ago" || r["anchor"] != "c-"+cid(35) {
			t.Errorf("row: %+v", r)
		}
		c = newcomments(t, "", map[string]string{"p": "2"})
		if rows = rowsOf(c); len(rows) != 5 || rows[4]["id"] != cid(1) || c["more_href"] != "" {
			t.Errorf("page 2: %d rows, more %q", len(rows), c["more_href"])
		}
	})

	t.Run("dead story and non-live comments are left out", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		f.comment(2, bob, 2).Entity = eid2
		f.comment(3, bob, 3).Live = false
		if rows := rowsOf(newcomments(t, "", nil)); len(rows) != 1 || rows[0]["id"] != cid(1) {
			t.Fatalf("rows: %+v", rows)
		}
	})

	t.Run("votes, own rows, downvote bar", func(t *testing.T) {
		f, ann, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		f.comment(2, bob, 2)
		f.comment(3, ann, 3)
		f.votes[cid(2)] = map[int64]*voteRow{ann.ID: {Value: 1}}
		by := map[string]map[string]any{}
		for _, r := range rowsOf(newcomments(t, "k1", nil)) {
			by[r["id"].(string)] = r
		}
		if r := by[cid(2)]; r["vote_cell"] != "hn-vote hn-voted" || r["can_un"] != true || r["can_up"] != false {
			t.Errorf("voted row: %+v", r)
		}
		if r := by[cid(1)]; r["vote"] != true || r["can_up"] != true || r["can_down"] != false || r["can_un"] != false || r["vote_cell"] != "hn-vote" {
			t.Errorf("plain row: %+v", r)
		}
		if r := by[cid(3)]; r["vote"] != false || r["can_up"] != false {
			t.Errorf("own row: %+v", r)
		}
		ann.Karma = 499
		if r := rowsOf(newcomments(t, "k1", nil))[2]; r["can_down"] != false {
			t.Errorf("karma 499: %+v", r)
		}
		ann.Karma = 500
		if r := rowsOf(newcomments(t, "k1", nil))[2]; r["can_down"] != true {
			t.Errorf("karma 500: %+v", r)
		}
	})

	t.Run("signed out may upvote but not downvote", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		r := rowsOf(newcomments(t, "", nil))[0]
		if r["can_up"] != true || r["can_down"] != false || r["can_un"] != false {
			t.Errorf("row: %+v", r)
		}
	})

	t.Run("faint and dead classes, indent, next", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1).Points = 0
		for i := 2; i <= 31; i++ {
			f.comment(i, bob, i)
		}
		c := newcomments(t, "", map[string]string{"p": "1"})
		rows := rowsOf(c)
		if rows[len(rows)-1]["row_class"] == "hn-c hn-faint" {
			t.Fatalf("oldest row should be on page 2: %+v", rows[len(rows)-1])
		}
		c = newcomments(t, "", map[string]string{"p": "2"})
		r := rowsOf(c)[0]
		if r["row_class"] != "hn-c hn-faint" || r["depth"] != "0" || r["next"] != "/newcomments?p=2" {
			t.Errorf("row: %+v", r)
		}
	})

	t.Run("placeholder id kept", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		ctx := sdk.PageContext{Path: "/newcomments", Blocks: []sdk.Block{{ID: "kit1", Type: "text"}, {ID: "ph", Type: "hn_threads"}}}
		out := renderPage(t, ctx)
		if len(out.Blocks) != 2 || out.Blocks[1].ID != "ph" || out.Blocks[1].Content == nil {
			t.Errorf("blocks: %+v", out.Blocks)
		}
	})

	t.Run("notice from the query", func(t *testing.T) {
		newcommentsFake(t)
		c := newcomments(t, "", map[string]string{"m": "comment-voted"})
		if c["notice"] != "You already voted on that comment." || c["empty"] != true {
			t.Errorf("content: %+v", c)
		}
	})

	t.Run("db error keeps the block and the page", func(t *testing.T) {
		f, _, _ := newcommentsFake(t)
		f.failSQL[sqlNewest] = errBoom
		c := newcomments(t, "", nil)
		if c["notice"] != textBroken || len(rowsOf(c)) != 0 || c["empty"] != false {
			t.Errorf("content: %+v", c)
		}
	})

	t.Run("no commenter key in the blocks, other pages untouched", func(t *testing.T) {
		f, _, bob := newcommentsFake(t)
		f.comment(1, bob, 1)
		ctx := sdk.PageContext{Path: "/newcomments", CommenterKey: "k1"}
		raw, _ := json.Marshal(renderPage(t, ctx).Blocks)
		if strings.Contains(string(raw), "k1") || strings.Contains(string(raw), "k2") {
			t.Errorf("key leaked: %s", raw)
		}
		if out := renderPage(t, sdk.PageContext{Path: "/news"}); len(out.Blocks) != 0 {
			t.Errorf("/news: %+v", out.Blocks)
		}
	})
}

var errBoom = fmt.Errorf("boom")
