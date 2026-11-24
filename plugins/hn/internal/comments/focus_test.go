package comments

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// focusFake: comment cid(3) has ancestors cid(1), cid(2), a reply cid(4) and a reply of that, cid(5).
func focusFake(t *testing.T) *fake {
	f, ann, bob := newcommentsFake(t)
	f.comment(1, bob, 1)
	f.comment(2, ann, 2)
	f.comment(3, bob, 3)
	f.comment(4, ann, 4)
	f.comment(5, bob, 5)
	f.subtrees[cid(3)] = sdk.CommentSubtree{
		Ancestors: []sdk.CommentItem{item(cid(1), "", "bob", "k2", 1), item(cid(2), cid(1), "ann", "k1", 2)},
		Items: []sdk.CommentItem{item(cid(3), cid(2), "bob", "k2", 3), item(cid(4), cid(3), "ann", "k1", 4),
			item(cid(5), cid(4), "bob", "k2", 5)},
	}
	return f
}

func focus(t *testing.T, path, key string, id string, blocks ...sdk.Block) sdk.PageContext {
	t.Helper()
	q := map[string]string{}
	if id != "" {
		q["id"] = id
	}
	return renderPage(t, sdk.PageContext{Path: path, CommenterKey: key, Query: q, Blocks: blocks})
}

func types(ctx sdk.PageContext) string {
	var ts []string
	for _, b := range ctx.Blocks {
		ts = append(ts, b.Type)
	}
	return strings.Join(ts, ",")
}

func TestThreadsPageFocus(t *testing.T) {
	story := []sdk.Block{{ID: "ub", Type: "hn_userbar"}, {ID: "it", Type: "hn_item"}, {ID: "x", Type: "text"}}

	t.Run("block goes right after hn_item", func(t *testing.T) {
		focusFake(t)
		if got := types(focus(t, "/item/7", "", cid(3), story...)); got != "hn_userbar,hn_item,hn_threads,text" {
			t.Errorf("blocks: %s", got)
		}
	})

	t.Run("no hn_item appends", func(t *testing.T) {
		focusFake(t)
		if got := types(focus(t, "/item/7", "", cid(3), story[0], story[2])); got != "hn_userbar,text,hn_threads" {
			t.Errorf("blocks: %s", got)
		}
	})

	t.Run("existing placeholder is filled in place", func(t *testing.T) {
		focusFake(t)
		ctx := focus(t, "/item/7", "", cid(3), story[0], sdk.Block{ID: "ph", Type: "hn_threads"}, story[1])
		if types(ctx) != "hn_userbar,hn_threads,hn_item" || ctx.Blocks[1].ID != "ph" {
			t.Errorf("blocks: %+v", ctx.Blocks)
		}
	})

	t.Run("without a valid id the page is untouched", func(t *testing.T) {
		f := focusFake(t)
		for _, id := range []string{"", "nope", "1; DROP TABLE x"} {
			ctx := focus(t, "/item/7", "k1", id, story...)
			if len(ctx.Blocks) != 3 || types(ctx) != "hn_userbar,hn_item,text" {
				t.Errorf("id %q: %s", id, types(ctx))
			}
		}
		if len(f.host.Calls) != 0 {
			t.Errorf("host calls: %v", f.host.Calls)
		}
	})

	t.Run("ancestors, comment and replies indented", func(t *testing.T) {
		focusFake(t)
		c := threadsBlock(t, focus(t, "/item/7", "k1", cid(3), story...))
		rows := rowsOf(c)
		if len(rows) != 5 || c["mode"] != "focus" {
			t.Fatalf("content: %+v", c)
		}
		for i, want := range []string{"0", "1", "2", "3", "4"} {
			if rows[i]["depth"] != want || rows[i]["id"] != cid(i+1) {
				t.Errorf("row %d: depth %v id %v", i, rows[i]["depth"], rows[i]["id"])
			}
		}
		if r := rows[2]; r["context_href"] != "/item/7?id="+cid(3) || r["parent_href"] != "/item/7?id="+cid(2) || r["item_href"] != "" || r["next"] != "/item/7?id="+cid(3) {
			t.Errorf("comment row: %+v", r)
		}
	})

	t.Run("own comment takes no vote", func(t *testing.T) {
		focusFake(t)
		rows := rowsOf(threadsBlock(t, focus(t, "/item/7", "k2", cid(3), story...)))
		if rows[2]["vote"] != false || rows[3]["vote"] != true {
			t.Errorf("rows: %+v %+v", rows[2], rows[3])
		}
	})

	t.Run("not found and unavailable", func(t *testing.T) {
		f := focusFake(t)
		f.subtreeErrFor[cid(3)] = &sdk.HostError{Code: sdk.ErrNotFound}
		if c := threadsBlock(t, focus(t, "/item/7", "", cid(3), story...)); c["notice"] != textNoComment || len(rowsOf(c)) != 0 {
			t.Errorf("not_found: %+v", c)
		}
		if c := threadsBlock(t, focus(t, "/item/7", "", cid(2), story...)); c["notice"] != textNoComment {
			t.Errorf("empty subtree: %+v", c)
		}
		delete(f.subtreeErrFor, cid(3))
		f.subtreeErr = &sdk.HostError{Code: sdk.ErrUnavailable}
		if c := threadsBlock(t, focus(t, "/item/7", "", cid(3), story...)); c["notice"] != textUnavailable {
			t.Errorf("unavailable: %+v", c)
		}
		f.subtreeErr = errBoom
		if c := threadsBlock(t, focus(t, "/item/7", "", cid(3), story...)); c["notice"] != textBroken {
			t.Errorf("error: %+v", c)
		}
	})

	t.Run("a path without a story number has no item links", func(t *testing.T) {
		focusFake(t)
		for _, r := range rowsOf(threadsBlock(t, focus(t, "/item/abc", "", cid(3), story...))) {
			if r["context_href"] != "" || r["parent_href"] != "" || r["item_href"] != "" {
				t.Errorf("row: %+v", r)
			}
		}
	})

	t.Run("no commenter key in the blocks", func(t *testing.T) {
		focusFake(t)
		raw, _ := json.Marshal(focus(t, "/item/7", "k1", cid(3), story...).Blocks)
		if strings.Contains(string(raw), "k1") || strings.Contains(string(raw), "k2") {
			t.Errorf("key leaked: %s", raw)
		}
	})
}
