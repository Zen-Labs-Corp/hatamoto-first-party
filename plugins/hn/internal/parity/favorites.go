package parity

import (
	"net/url"
	"strconv"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// $1 viewer id (0 signed out), $2 list owner, $3 limit, $4 offset.
var (
	sqlFavStories = storyList("favorites f JOIN story_meta s ON s.entity_id = f.target_id",
		"f.user_id = $2 AND f.kind = 'story' AND NOT s.dead", "f.created_at DESC")
	sqlHiddenStories = storyList("hidden h JOIN story_meta s ON s.entity_id = h.story_id",
		"h.user_id = $2 AND NOT s.dead", "h.created_at DESC")
)

// $1 list owner, $2 limit, $3 offset. The excerpt is the plain text kept for
// search; the HTML body never reaches a view.
const sqlFavComments = "SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.ord), '[]'::jsonb) FROM (SELECT row_number() OVER (ORDER BY f.created_at DESC) AS ord," +
	" c.comment_id, c.entity_id, s.item_no, coalesce(u.username, c.author_name) AS by, s.title AS story_title, left(coalesce(d.body, ''), 200) AS excerpt, c.created_at" +
	" FROM favorites f JOIN comment_meta c ON c.comment_id = f.target_id AND c.live JOIN story_meta s ON s.entity_id = c.entity_id AND NOT s.dead" +
	" LEFT JOIN users u ON u.id = c.author_id LEFT JOIN search_docs d ON d.kind = 'comment' AND d.target_id = c.comment_id" +
	" WHERE f.user_id = $1 AND f.kind = 'comment' ORDER BY f.created_at DESC LIMIT $2 OFFSET $3) r"

// favoritesPage fills hn_saved on /favorites: a member's favorite stories (or
// comments with kind=comment), or the viewer's own hidden stories with
// view=hidden. It loads first and places last, so a host failure leaves the
// page as it was.
func favoritesPage(r *core.Registry, p *core.Page) error {
	v, page := p.Viewer(), parsePage(p.Query("p"))
	c := map[string]any{"state": "list", "notice": core.NoticeText(p.Query("m")), "rows": []map[string]any{}, "comments": []map[string]any{}, "stories_href": "", "comments_href": "", "more": "", "empty": false}
	login := func() error {
		c["state"], c["login_href"] = "login", core.WithNotice("/login", nLogin)
		place(p, "hn_saved", c, true)
		return nil
	}
	var viewerID int64
	if v != nil {
		viewerID = v.ID
	}

	hidden := p.Query("view") == "hidden"
	owner := v
	if !hidden {
		if id := strings.TrimSpace(p.Query("id")); id != "" {
			u, err := core.UserByName(id)
			if err != nil {
				return nil
			}
			if u == nil {
				c["state"] = "nouser"
				place(p, "hn_saved", c, true)
				return nil
			}
			owner = u
		}
	}
	if owner == nil {
		return login()
	}

	base := "/favorites?id=" + url.QueryEscape(owner.Username)
	kind, heading := "story", owner.Username+"'s favorites"
	switch {
	case hidden:
		base, heading = "/favorites?view=hidden", "Hidden stories"
	case p.Query("kind") == "comment":
		kind, heading = "comment", owner.Username+"'s favorite comments"
		base += "&kind=comment"
	}
	if !hidden {
		c["stories_href"], c["comments_href"] = "/favorites?id="+url.QueryEscape(owner.Username), "/favorites?id="+url.QueryEscape(owner.Username)+"&kind=comment"
	}
	self := base
	if page > 1 {
		self += "&p=" + strconv.Itoa(page)
	}
	offset := (page - 1) * pageSize

	var n int
	switch {
	case kind == "comment":
		var rows []commentRow
		if err := core.QueryJSON(sqlFavComments, &rows, owner.ID, pageSize+1, offset); err != nil {
			return nil
		}
		n = len(rows)
		views := make([]map[string]any, 0, min(n, pageSize))
		for _, row := range rows[:min(n, pageSize)] {
			views = append(views, commentView(row))
		}
		c["comments"] = views
	default:
		sql := sqlFavStories
		if hidden {
			sql = sqlHiddenStories
		}
		var rows []storyRow
		if err := core.QueryJSON(sql, &rows, viewerID, owner.ID, pageSize+1, offset); err != nil {
			return nil
		}
		n = len(rows)
		views := make([]map[string]any, 0, min(n, pageSize))
		for i, row := range rows[:min(n, pageSize)] {
			views = append(views, storyView(r, v, row, offset+i+1, self))
		}
		c["rows"] = views
	}
	c["kind"], c["heading"], c["empty"] = kind, heading, n == 0
	if n > pageSize {
		c["more"] = base + "&p=" + strconv.Itoa(page+1)
	}
	place(p, "hn_saved", c, true)
	return nil
}
