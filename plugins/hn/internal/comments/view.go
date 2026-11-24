package comments

import (
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// rowIn is one comment as a view receives it.
type rowIn struct {
	ID, ParentID, Author string
	AuthorID             *int64
	Own, Dead            bool
	Body                 string
	Points               int
	Created              time.Time
	ItemNo               int64
	Title                string
	Depth                int
}

// voteView is what the vote cells of one page need.
type voteView struct {
	viewer *core.User
	voted  map[string]int
	downOK bool
	parts  *int // body parts the page may still print
}

// newVoteView reads the viewer's votes on ids and the downvote bar once for the page.
func newVoteView(viewer *core.User, ids []string) voteView {
	left := pageParts
	vv := voteView{viewer: viewer, voted: myVotes(viewer, ids), parts: &left}
	if viewer != nil {
		vv.downOK = viewer.Karma >= core.LoadSettings().DownvoteKarma
	}
	return vv
}

const sqlMyVotes = `SELECT coalesce(jsonb_object_agg(comment_id::text, value), '{}') FROM comment_votes WHERE user_id = $1 AND comment_id IN (SELECT jsonb_array_elements_text($2::jsonb)::uuid)`

// myVotes maps comment id to the viewer's vote value. No viewer, no ids or a
// host error give an empty map (fail open).
func myVotes(viewer *core.User, ids []string) map[string]int {
	out := map[string]int{}
	if viewer == nil || len(ids) == 0 {
		return out
	}
	var got map[string]int
	if err := core.QueryJSON(sqlMyVotes, &got, viewer.ID, jsonList(ids)); err == nil && got != nil {
		out = got
	}
	return out
}

func itemHref(n int64) string {
	if n == 0 {
		return ""
	}
	return "/item/" + strconv.FormatInt(n, 10)
}

func focusHref(n int64, id string) string {
	if n == 0 || id == "" {
		return ""
	}
	return itemHref(n) + "?id=" + id
}

// row builds the flat map every view prints. All keys are always present and
// numbers are formatted, so templates never meet a missing key or a float.
func row(r *core.Registry, in rowIn, vv voteView, next string) map[string]any {
	voted := vv.voted[in.ID] != 0
	vote := !in.Dead && !in.Own
	canUp := vote && !voted
	class, cell := "hn-c", "hn-vote"
	if in.Dead {
		class = "hn-c hn-dead"
	} else if in.Points < 1 {
		class = "hn-c hn-faint"
	}
	if voted {
		cell = "hn-vote hn-voted"
	}
	var authorID int64
	if in.AuthorID != nil {
		authorID = *in.AuthorID
	}
	acts := []map[string]any{}
	for _, a := range r.ActionsFor(vv.viewer, core.Item{Kind: "comment", ID: in.ID, ItemNo: in.ItemNo, AuthorID: authorID, Dead: in.Dead}) {
		acts = append(acts, map[string]any{"label": a.Label, "href": a.Href, "class": a.Class, "post": a.Post})
	}
	context := focusHref(in.ItemNo, in.ID)
	return map[string]any{
		"id": in.ID, "anchor": "c-" + in.ID,
		"author": in.Author, "author_href": "/user?id=" + url.QueryEscape(in.Author),
		"ago":          ago(in.Created),
		"context_href": context, "parent_href": focusHref(in.ItemNo, in.ParentID),
		"item_href": itemHref(in.ItemNo), "title": in.Title, "reply_href": context,
		"body": bodyBlocksIn(in.Body, vv.parts), "row_class": class, "depth": strconv.Itoa(in.Depth),
		"replies": []map[string]any{}, "actions": acts,
		"vote": vote, "vote_cell": cell,
		"can_up": canUp, "can_down": canUp && vv.downOK, "can_un": vote && voted,
		"next": next,
	}
}

// content is the block content of every view; callers add nothing but may
// override empty for a failed read.
func content(mode, notice, heading string, rows []map[string]any, more string) map[string]any {
	return map[string]any{"mode": mode, "notice": notice, "heading": heading, "rows": rows, "empty": len(rows) == 0, "more_href": more}
}

// ago words the age of a comment against the plugin clock.
func ago(t time.Time) string {
	d := core.Now().Sub(t)
	unit := func(n int, name string) string {
		if n == 1 {
			return "1 " + name + " ago"
		}
		return strconv.Itoa(n) + " " + name + "s ago"
	}
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return unit(int(d/time.Minute), "minute")
	case d < 24*time.Hour:
		return unit(int(d/time.Hour), "hour")
	}
	return unit(int(d/(24*time.Hour)), "day")
}

// jsonList is the one text argument that SQL expands with jsonb_array_elements_text.
func jsonList(ids []string) string {
	b, _ := json.Marshal(ids)
	return string(b)
}
