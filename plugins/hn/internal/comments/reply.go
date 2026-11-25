package comments

import (
	"errors"
	"maps"
	"slices"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const (
	textNoItem = "No such item."

	// replyMaxDepth is the comment policy max_depth of the hn kit. Core depth is
	// 1-based and offers reply only while depth < max; the plugin cannot read
	// that setting.
	replyMaxDepth = 32

	sqlReplyParent = `SELECT to_jsonb(x) FROM (SELECT m.entity_id, m.live, s.item_no, s.title, s.dead FROM comment_meta m JOIN story_meta s ON s.entity_id = m.entity_id WHERE m.comment_id = $1::uuid) x`
)

// replyRef is the story a comment belongs to and whether it can still be replied to.
type replyRef struct {
	EntityID string `json:"entity_id"`
	Live     bool   `json:"live"`
	ItemNo   int64  `json:"item_no"`
	Title    string `json:"title"`
	Dead     bool   `json:"dead"`
}

// replyContent is the hn_reply content with every key present. state is one of
// none, form, noname, login or deep.
func replyContent(state string) map[string]any {
	return map[string]any{"state": state, "text": "", "notice": "", "row": nil, "action": "", "parent": "", "login_href": ""}
}

// placeReply fills the page's hn_reply placeholder, keeping its ID, or appends the block.
func placeReply(p *core.Page, c map[string]any) {
	const typ = "hn_reply"
	i, id := p.Find(typ), typ
	if i >= 0 && p.Ctx.Blocks[i].ID != "" {
		id = p.Ctx.Blocks[i].ID
	}
	p.Replace(i, sdk.Block{ID: id, Type: typ, Content: c})
}

// replyPage fills hn_reply on /reply?id=<comment>: the parent comment and, for
// a named viewer, a form core fills and accepts (POST /-/comments/<story>).
// Core turns a reply to a gone or unapproved parent into a top-level comment,
// so those parents show "No such item." here. It never fails the page.
func replyPage(r *core.Registry) func(*core.Page) error {
	return func(p *core.Page) error {
		none := func(text string) {
			c := replyContent("none")
			c["text"] = text
			placeReply(p, c)
		}
		id := strings.ToLower(p.Query("id"))
		if !validID(id) {
			none(textNoItem)
			return nil
		}
		var ref replyRef
		switch err := core.QueryJSON(sqlReplyParent, &ref, id); {
		case errors.Is(err, core.ErrNoRows):
			none(textNoItem)
			return nil
		case err != nil:
			none(textBroken)
			return nil
		}
		if !ref.Live || ref.Dead || !validID(ref.EntityID) {
			none(textNoItem)
			return nil
		}
		st, err := subtree(id)
		switch {
		case isUnavailable(err):
			none(textUnavailable)
			return nil
		case hostCode(err, sdk.ErrNotFound):
			none(textNoItem)
			return nil
		case err != nil:
			none(textBroken)
			return nil
		}
		at := slices.IndexFunc(st.Items, func(x sdk.CommentItem) bool { return strings.EqualFold(x.ID, id) })
		if at < 0 || st.Items[at].Status != "approved" {
			none(textNoItem)
			return nil
		}

		x, viewer := st.Items[at], p.Viewer()
		next := "/reply?id=" + id
		own := viewer != nil && x.CommenterKey != "" && x.CommenterKey == viewer.CommenterKey
		out := row(r, rowIn{ID: x.ID, ParentID: x.ParentID, Author: x.AuthorName, AuthorID: storyRefs([]string{id})[id].AuthorID, Own: own,
			Body: x.BodyHTML, Points: 1, Created: x.CreatedAt, ItemNo: ref.ItemNo, Title: ref.Title}, newVoteView(viewer, []string{id}), next)
		out["reply_href"], out["context_link"] = "", true

		c := replyContent("form")
		c["row"], c["notice"] = out, core.NoticeText(p.Query("m"))
		switch {
		case len(st.Ancestors)+1 >= replyMaxDepth:
			c["state"] = "deep"
		case viewer == nil && p.Ctx.CommenterKey == "":
			maps.Copy(c, core.LoginForms(next))
			c["state"] = "login"
		case viewer == nil:
			c["state"], c["login_href"] = "noname", "/login"
		default:
			c["action"], c["parent"] = "/-/comments/"+ref.EntityID, id
		}
		placeReply(p, c)
		return nil
	}
}
