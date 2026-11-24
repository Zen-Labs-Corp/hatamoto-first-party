package parity

import (
	"net/url"
	"strconv"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// storyRow is one story of a list; By is the author's username.
type storyRow struct {
	EntityID     string     `json:"entity_id"`
	ItemNo       int64      `json:"item_no"`
	AuthorID     int64      `json:"author_id"`
	By           string     `json:"by"`
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	Domain       string     `json:"domain"`
	Kind         string     `json:"kind"`
	Points       int        `json:"points"`
	CommentCount int        `json:"comment_count"`
	Dead         bool       `json:"dead"`
	CreatedAt    time.Time  `json:"created_at"`
	VotedAt      *time.Time `json:"voted_at"`
}

// commentRow is one comment of a list, with a plain-text excerpt (never the HTML body).
type commentRow struct {
	CommentID  string    `json:"comment_id"`
	EntityID   string    `json:"entity_id"`
	ItemNo     int64     `json:"item_no"`
	By         string    `json:"by"`
	StoryTitle string    `json:"story_title"`
	Excerpt    string    `json:"excerpt"`
	CreatedAt  time.Time `json:"created_at"`
}

// storyColumns is the select list of a story row; $1 is the viewer id (for the vote).
const storyColumns = "s.entity_id, s.item_no, s.author_id, u.username AS by, s.title, s.url, s.domain, s.kind, s.points, s.comment_count, s.dead, s.created_at, v.created_at AS voted_at"

// storyList wraps a story query: from/where over s, u, v with $1 the viewer id,
// $2 the list owner and $3/$4 limit and offset, ordered by order.
func storyList(from, where, order string) string {
	return "SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.ord), '[]'::jsonb) FROM (SELECT row_number() OVER (ORDER BY " + order + ") AS ord, " + storyColumns +
		" FROM " + from + " JOIN users u ON u.id = s.author_id LEFT JOIN story_votes v ON v.story_id = s.entity_id AND v.user_id = $1" +
		" WHERE " + where + " ORDER BY " + order + " LIMIT $3 OFFSET $4) r"
}

// storyView is the template map of one story row; next is the page the vote
// form returns to. rank 0 leaves the rank empty. The actions are the ones
// other phases register through core.
func storyView(r *core.Registry, v *core.User, s storyRow, rank int, next string) map[string]any {
	no := strconv.FormatInt(s.ItemNo, 10)
	itemHref := "/item/" + no
	m := map[string]any{
		"no": no, "title": s.Title, "href": itemHref, "external": s.URL != "", "site": s.Domain,
		"site_href": "/from?site=" + url.QueryEscape(s.Domain), "vote_href": "/p/hn/vote/story/" + no, "next": next,
		"job": s.Kind == "job", "by": s.By, "user_href": "/user?id=" + url.QueryEscape(s.By),
		"age": ageText(core.Now().Sub(s.CreatedAt)), "item_href": itemHref, "rank": "", "score": "", "comments": "",
	}
	if s.URL != "" {
		m["href"] = s.URL
	}
	if s.Dead {
		m["title"] = "[dead] " + s.Title
	}
	if rank > 0 {
		m["rank"] = strconv.Itoa(rank) + "."
	}
	if s.Kind != "job" {
		m["score"], m["comments"] = pointsLabel(s.Points), commentsLabel(s.CommentCount)
	}
	switch {
	case s.Kind == "job" || (v != nil && v.ID == s.AuthorID) || s.Dead:
		m["vote"] = "none"
	case s.VotedAt != nil:
		m["vote"] = "voted"
	default:
		m["vote"] = "up"
	}
	var acts []map[string]any
	for _, a := range r.ActionsFor(v, core.Item{Kind: "story", ID: s.EntityID, ItemNo: s.ItemNo, AuthorID: s.AuthorID, Dead: s.Dead}) {
		acts = append(acts, map[string]any{"label": a.Label, "href": a.Href, "class": a.Class, "post": a.Post})
	}
	m["actions"] = acts
	return m
}

// commentView is the template map of one comment row.
func commentView(c commentRow) map[string]any {
	return map[string]any{
		"by": c.By, "user_href": "/user?id=" + url.QueryEscape(c.By),
		"age":         ageText(core.Now().Sub(c.CreatedAt)),
		"item_href":   "/item/" + strconv.FormatInt(c.ItemNo, 10) + "#" + c.CommentID,
		"story_title": c.StoryTitle, "excerpt": c.Excerpt,
	}
}

// place fills the first block of type typ, keeping its ID and title; with none
// it appends a block when appendOK. It never prepends: the accounts bar goes first.
func place(p *core.Page, typ string, c map[string]any, appendOK bool) {
	if i := p.Find(typ); i >= 0 {
		b := p.Ctx.Blocks[i]
		p.Replace(i, sdk.Block{ID: b.ID, Type: typ, Title: b.Title, Content: c})
		return
	}
	if appendOK {
		p.Append(sdk.Block{ID: typ, Type: typ, Content: c})
	}
}
