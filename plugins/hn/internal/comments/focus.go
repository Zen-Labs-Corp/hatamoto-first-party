package comments

import (
	"slices"
	"strconv"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const textNoComment = "No such comment."

// focusPage shows one comment of a story page (/item/<n>?id=<comment>) with
// its ancestors and replies, right after the story block. A page without a
// valid id is left to the stories phase.
func focusPage(r *core.Registry) func(*core.Page) error {
	return func(p *core.Page) error {
		id := strings.ToLower(p.Query("id"))
		if !validID(id) {
			return nil
		}
		var itemNo int64
		if n, err := strconv.ParseInt(strings.TrimPrefix(p.Ctx.Path, "/item/"), 10, 64); err == nil && n > 0 {
			itemNo = n
		}
		put := func(c map[string]any) {
			insertAfterItem(p, sdk.Block{ID: "hn_threads", Type: "hn_threads", Content: c})
		}
		fail := func(text string) {
			c := content("focus", text, "", []map[string]any{}, "")
			c["empty"] = false
			put(c)
		}

		st, err := subtree(id)
		switch {
		case isUnavailable(err):
			fail(textUnavailable)
			return nil
		case hostCode(err, sdk.ErrNotFound):
			fail(textNoComment)
			return nil
		case err != nil:
			fail(textBroken)
			return nil
		}
		at := slices.IndexFunc(st.Items, func(x sdk.CommentItem) bool { return strings.EqualFold(x.ID, id) })
		if at < 0 {
			fail(textNoComment)
			return nil
		}

		// Ancestors first, then the comment, then its replies, each indented by depth.
		type entry struct {
			it    sdk.CommentItem
			depth int
		}
		var list []entry
		for i, a := range st.Ancestors {
			list = append(list, entry{a, i})
		}
		top := len(st.Ancestors)
		depth := map[string]int{st.Items[at].ID: top}
		list = append(list, entry{st.Items[at], top})
		for i, x := range st.Items {
			if i == at {
				continue
			}
			d, ok := depth[x.ParentID]
			if !ok {
				d = top
			}
			depth[x.ID] = d + 1
			list = append(list, entry{x, d + 1})
		}

		ids := make([]string, len(list))
		for i, e := range list {
			ids[i] = e.it.ID
		}
		viewer := p.Viewer()
		refs, vv := storyRefs(ids), newVoteView(viewer, validIDs(ids))
		next := p.Ctx.Path + "?id=" + id
		rows := make([]map[string]any, 0, len(list))
		for _, e := range list {
			x := e.it
			own := viewer != nil && x.CommenterKey != "" && x.CommenterKey == viewer.CommenterKey
			out := row(r, rowIn{ID: x.ID, ParentID: x.ParentID, Author: x.AuthorName, AuthorID: refs[x.ID].AuthorID, Own: own,
				Dead: x.Status == "dead", Body: x.BodyHTML, Points: 1, Created: x.CreatedAt, ItemNo: itemNo, Depth: e.depth}, vv, next)
			out["item_href"] = "" // the story is already on the page
			rows = append(rows, out)
		}
		put(content("focus", core.NoticeText(p.Query("m")), "", rows, ""))
		return nil
	}
}

// insertAfterItem places b right after the story block (hn_item), replaces an
// existing hn_threads block in place, else appends. It never goes first: the
// user bar must stay on top.
func insertAfterItem(p *core.Page, b sdk.Block) {
	if i := p.Find("hn_threads"); i >= 0 {
		if id := p.Ctx.Blocks[i].ID; id != "" {
			b.ID = id
		}
		p.Replace(i, b)
		return
	}
	if i := p.Find("hn_item"); i >= 0 {
		p.Ctx.Blocks = slices.Insert(p.Ctx.Blocks, i+1, b)
		return
	}
	p.Append(b)
}
