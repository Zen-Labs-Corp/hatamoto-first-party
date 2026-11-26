package comments

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func item(id, parent, author, key string, minute int) sdk.CommentItem {
	return sdk.CommentItem{ID: id, ParentID: parent, AuthorName: author, CommenterKey: key, Status: "approved",
		BodyHTML: "<p>b " + id[len(id)-4:] + "</p>", CreatedAt: epoch.Add(time.Duration(minute) * time.Minute)}
}

// threadsFake has bob (k2) with n comments cid(1..n), newest = cid(n), listed by comments.by_author.
// Each has two direct replies by ann (k1) and one reply to the first reply.
func threadsFake(t *testing.T, n int) *fake {
	f, ann, bob := newcommentsFake(t)
	for i := n; i >= 1; i-- {
		f.comment(i, bob, i)
		own := item(cid(i), "", "bob", "k2", i)
		f.byAuthor["k2"] = append(f.byAuthor["k2"], own)
		r1, r2, g := cid(1000+i*10+1), cid(1000+i*10+2), cid(1000+i*10+3)
		for _, id := range []string{r1, r2, g} {
			f.meta[id] = &metaRow{ID: id, Entity: eid1, Parent: cid(i), Author: &ann.ID, Name: "ann", Live: true, Points: 1}
		}
		f.subtrees[cid(i)] = sdk.CommentSubtree{Items: []sdk.CommentItem{
			own, item(r1, cid(i), "ann", "k1", i), item(g, r1, "bob", "k2", i), item(r2, cid(i), "ann", "k1", i),
		}}
	}
	return f
}

func threads(t *testing.T, key string, q map[string]string) map[string]any {
	t.Helper()
	ctx := sdk.PageContext{Path: "/threads", CommenterKey: key, Query: q}
	return threadsBlock(t, renderPage(t, ctx))
}

func repliesOf(r map[string]any) []map[string]any {
	var out []map[string]any
	for _, x := range r["replies"].([]any) {
		out = append(out, x.(map[string]any))
	}
	return out
}

func TestThreadsPageThreads(t *testing.T) {
	t.Run("rows newest first with direct replies only", func(t *testing.T) {
		threadsFake(t, 3)
		c := threads(t, "k1", map[string]string{"id": "Bob"})
		rows := rowsOf(c)
		if len(rows) != 3 || rows[0]["id"] != cid(3) || rows[2]["id"] != cid(1) {
			t.Fatalf("rows: %+v", rows)
		}
		if c["heading"] != "bob's comments" || c["mode"] != "threads" || c["more_href"] != "" {
			t.Errorf("content: %+v", c)
		}
		for _, r := range rows {
			kids := repliesOf(r)
			if len(kids) != 2 || kids[0]["depth"] != "1" || kids[0]["author"] != "ann" || r["depth"] != "0" {
				t.Errorf("replies of %v: %+v", r["id"], kids)
			}
			if r["item_href"] != "/item/7" || r["title"] != "Seven" || kids[0]["item_href"] != "/item/7" {
				t.Errorf("story links: %+v", r)
			}
			if r["next"] != "/threads?id=bob#comment-"+r["id"].(string) {
				t.Errorf("next: %v", r["next"])
			}
		}
	})

	t.Run("paging and the SDK page boundary", func(t *testing.T) {
		f := threadsFake(t, 70)
		c := threads(t, "", map[string]string{"id": "bob"})
		if rows := rowsOf(c); len(rows) != 15 || rows[0]["id"] != cid(70) || c["more_href"] != "/threads?id=bob&p=2" {
			t.Fatalf("p1: %d rows, more %q", len(rows), c["more_href"])
		}
		if len(f.subtreeCalls) > 15 || len(f.byAuthorCalls) != 1 {
			t.Errorf("p1 calls: %d subtree, %v by_author", len(f.subtreeCalls), f.byAuthorCalls)
		}
		f.byAuthorCalls, f.subtreeCalls = nil, nil
		c = threads(t, "", map[string]string{"id": "bob", "p": "4"})
		rows := rowsOf(c)
		if len(rows) != 15 || rows[0]["id"] != cid(70-45) || rows[14]["id"] != cid(70-59) {
			t.Fatalf("p4: %d rows, first %v", len(rows), rows[0]["id"])
		}
		if len(f.byAuthorCalls) != 2 || len(f.subtreeCalls) > 15 {
			t.Errorf("p4 calls: %v, %d subtree", f.byAuthorCalls, len(f.subtreeCalls))
		}
		if c["more_href"] != "/threads?id=bob&p=5" || rows[0]["next"] != "/threads?id=bob&p=4#comment-"+rows[0]["id"].(string) {
			t.Errorf("p4 hrefs: %q %q", c["more_href"], rows[0]["next"])
		}
		c = threads(t, "", map[string]string{"id": "bob", "p": "5"})
		if rows = rowsOf(c); len(rows) != 10 || c["more_href"] != "" {
			t.Errorf("p5: %d rows, more %q", len(rows), c["more_href"])
		}
	})

	t.Run("unknown user and empty id make no comments call", func(t *testing.T) {
		f := threadsFake(t, 1)
		for _, q := range []map[string]string{{"id": "nobody"}, {}} {
			c := threads(t, "", q)
			if c["notice"] != textNoUser || len(rowsOf(c)) != 0 || c["empty"] != false {
				t.Errorf("%v: %+v", q, c)
			}
		}
		for _, call := range f.host.Calls {
			if strings.HasPrefix(call, "comments.") {
				t.Errorf("host call %s", call)
			}
		}
	})

	t.Run("by_author unavailable", func(t *testing.T) {
		f := threadsFake(t, 1)
		f.byAuthorErr = &sdk.HostError{Code: sdk.ErrUnavailable}
		c := threads(t, "", map[string]string{"id": "bob"})
		if c["notice"] != textUnavailable || len(rowsOf(c)) != 0 {
			t.Errorf("content: %+v", c)
		}
		f.byAuthorErr = errBoom
		if c = threads(t, "", map[string]string{"id": "bob"}); c["notice"] != textBroken {
			t.Errorf("content: %+v", c)
		}
	})

	t.Run("subtree unavailable keeps the rows", func(t *testing.T) {
		f := threadsFake(t, 3)
		f.subtreeErr = &sdk.HostError{Code: sdk.ErrUnavailable}
		rows := rowsOf(threads(t, "", map[string]string{"id": "bob"}))
		if len(rows) != 3 || len(repliesOf(rows[0])) != 0 || len(f.subtreeCalls) != 1 {
			t.Errorf("%d rows, %d subtree calls", len(rows), len(f.subtreeCalls))
		}
	})

	t.Run("subtree error leaves only that row bare", func(t *testing.T) {
		f := threadsFake(t, 3)
		f.subtreeErrFor[cid(2)] = errBoom
		rows := rowsOf(threads(t, "", map[string]string{"id": "bob"}))
		if len(repliesOf(rows[0])) != 2 || len(repliesOf(rows[1])) != 0 || len(repliesOf(rows[2])) != 2 {
			t.Errorf("replies: %d %d %d", len(repliesOf(rows[0])), len(repliesOf(rows[1])), len(repliesOf(rows[2])))
		}
	})

	t.Run("own rows and own replies take no vote", func(t *testing.T) {
		threadsFake(t, 1)
		rows := rowsOf(threads(t, "k2", map[string]string{"id": "bob"}))
		if rows[0]["vote"] != false || repliesOf(rows[0])[0]["vote"] != true {
			t.Errorf("as author: %+v", rows[0])
		}
		rows = rowsOf(threads(t, "k1", map[string]string{"id": "bob"}))
		if rows[0]["vote"] != true || repliesOf(rows[0])[0]["vote"] != false {
			t.Errorf("as replier: %+v", rows[0])
		}
	})

	t.Run("dead reply", func(t *testing.T) {
		f := threadsFake(t, 1)
		st := f.subtrees[cid(1)]
		st.Items[1].Status = "dead"
		rows := rowsOf(threads(t, "", map[string]string{"id": "bob"}))
		if r := repliesOf(rows[0])[0]; r["row_class"] != "hn-c hn-dead" || r["vote"] != false || r["reply_href"] != "" || r["context_href"] != "/item/7?id="+r["id"].(string) {
			t.Errorf("reply: %+v", r)
		}
	})

	t.Run("live rows reply on the reply page", func(t *testing.T) {
		threadsFake(t, 1)
		rows := rowsOf(threads(t, "", map[string]string{"id": "bob"}))
		for _, r := range append([]map[string]any{rows[0]}, repliesOf(rows[0])...) {
			id := r["id"].(string)
			if r["reply_href"] != "/reply?id="+id || r["context_href"] != "/item/7?id="+id || r["context_link"] != false {
				t.Errorf("row: %+v", r)
			}
		}
	})

	t.Run("votes of the viewer show", func(t *testing.T) {
		f := threadsFake(t, 1)
		f.votes[cid(1)] = map[int64]*voteRow{1: {Value: 1}}
		rows := rowsOf(threads(t, "k1", map[string]string{"id": "bob"}))
		if rows[0]["can_un"] != true || rows[0]["vote_cell"] != "hn-vote hn-voted" {
			t.Errorf("row: %+v", rows[0])
		}
	})

	t.Run("no commenter key in the blocks", func(t *testing.T) {
		threadsFake(t, 2)
		ctx := sdk.PageContext{Path: "/threads", CommenterKey: "k1", Query: map[string]string{"id": "bob"}}
		raw, _ := json.Marshal(renderPage(t, ctx).Blocks)
		if strings.Contains(string(raw), "k1") || strings.Contains(string(raw), "k2") {
			t.Errorf("key leaked: %s", raw)
		}
	})
}
