package parity

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const maxQ = 100 // bytes of a search query

// sqlSearch is ONE statement: stories first (grp 0), then comments (grp 1),
// each ranked by ts_rank, then newest. $1 query text, $2 viewer id (0 signed
// out), $3 limit, $4 offset. The query is only ever a bind argument.
const sqlSearch = `SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.grp, r.rank DESC, r.created_at DESC), '[]'::jsonb) FROM (
SELECT * FROM (
 SELECT 0 AS grp, ts_rank(d.tsv, q.t) AS rank, s.entity_id, s.item_no, s.author_id, u.username AS by, s.title, s.url, s.domain, s.kind, s.points, s.comment_count, s.dead, s.created_at, v.created_at AS voted_at, NULL::uuid AS comment_id, '' AS excerpt
 FROM search_docs d CROSS JOIN (SELECT websearch_to_tsquery('simple', $1::text) AS t) q
 JOIN story_meta s ON s.entity_id = d.target_id JOIN users u ON u.id = s.author_id
 LEFT JOIN story_votes v ON v.story_id = s.entity_id AND v.user_id = $2
 WHERE d.kind = 'story' AND d.tsv @@ q.t AND NOT s.dead
   AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.story_id = s.entity_id AND h.user_id = $2)
 UNION ALL
 SELECT 1, ts_rank(d.tsv, q.t), s.entity_id, s.item_no, c.author_id, coalesce(cu.username, c.author_name), s.title, '', '', s.kind, 0, 0, false, c.created_at, NULL, c.comment_id, left(d.body, 200)
 FROM search_docs d CROSS JOIN (SELECT websearch_to_tsquery('simple', $1::text) AS t) q
 JOIN comment_meta c ON c.comment_id = d.target_id AND c.live
 JOIN story_meta s ON s.entity_id = c.entity_id AND NOT s.dead
 LEFT JOIN users cu ON cu.id = c.author_id
 WHERE d.kind = 'comment' AND d.tsv @@ q.t
) a ORDER BY grp, rank DESC, created_at DESC LIMIT $3 OFFSET $4) r`

// cleanQ trims a query and cuts it to maxQ bytes on a rune boundary.
func cleanQ(s string) string {
	s = strings.TrimSpace(s)
	if len(s) > maxQ {
		n := maxQ
		for n > 0 && !utf8.RuneStart(s[n]) {
			n--
		}
		s = strings.TrimSpace(s[:n])
	}
	return s
}

// hit is one row of sqlSearch: a story (Grp 0) or a comment (Grp 1).
type hit struct {
	Grp int `json:"grp"`
	storyRow
	CommentID *string `json:"comment_id"`
	Excerpt   string  `json:"excerpt"`
}

// searchPage fills hn_search on /search. An empty query shows the form only
// and runs no query; a host failure leaves the page as it was.
func searchPage(r *core.Registry, p *core.Page) error {
	q, page := cleanQ(p.Query("q")), parsePage(p.Query("p"))
	c := map[string]any{"q": q, "action": "/search", "notice": core.NoticeText(p.Query("m")), "rows": []map[string]any{}, "comments": []map[string]any{}, "more": "", "empty": false}
	if q == "" {
		place(p, "hn_search", c, true)
		return nil
	}
	v := p.Viewer()
	var viewerID int64
	if v != nil {
		viewerID = v.ID
	}
	offset := (page - 1) * pageSize
	var hits []hit
	if err := core.QueryJSON(sqlSearch, &hits, q, viewerID, pageSize+1, offset); err != nil {
		return nil
	}
	base := "/search?q=" + url.QueryEscape(q)
	self := base
	if page > 1 {
		self += "&p=" + strconv.Itoa(page)
	}
	c["empty"] = len(hits) == 0
	if len(hits) > pageSize {
		hits = hits[:pageSize]
		c["more"] = base + "&p=" + strconv.Itoa(page+1)
	}
	rows, comments := []map[string]any{}, []map[string]any{}
	for i, h := range hits {
		if h.Grp == 0 {
			rows = append(rows, storyView(r, v, h.storyRow, offset+i+1, self))
			continue
		}
		id := ""
		if h.CommentID != nil {
			id = *h.CommentID
		}
		comments = append(comments, commentView(commentRow{CommentID: id, EntityID: h.EntityID, ItemNo: h.ItemNo, By: h.By, StoryTitle: h.Title, Excerpt: h.Excerpt, CreatedAt: h.CreatedAt}))
	}
	c["rows"], c["comments"] = rows, comments
	place(p, "hn_search", c, true)
	return nil
}
