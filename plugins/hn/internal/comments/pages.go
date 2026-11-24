package comments

import (
	"strconv"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const (
	newPageSize = 30
	maxPage     = 50

	textBroken      = "Something went wrong, try again."
	textUnavailable = "Not available yet."
)

// place fills the page's placeholder block of type hn_threads, keeping its ID,
// or appends the block when the page has none.
func place(p *core.Page, c map[string]any) {
	const typ = "hn_threads"
	i, id := p.Find(typ), typ
	if i >= 0 && p.Ctx.Blocks[i].ID != "" {
		id = p.Ctx.Blocks[i].ID
	}
	p.Replace(i, sdk.Block{ID: id, Type: typ, Content: c})
}

// pageNo is ?p= within 1..maxPage, else 1.
func pageNo(p *core.Page) int {
	n, err := strconv.Atoi(p.Query("p"))
	if err != nil || n < 1 || n > maxPage {
		return 1
	}
	return n
}

// pagedHref is base + "?p=N" (N > 1), or base alone for the first page.
func pagedHref(base string, n int) string {
	if n <= 1 {
		return base
	}
	return base + "?p=" + strconv.Itoa(n)
}

const sqlNewest = `SELECT coalesce(jsonb_agg(x), '[]') FROM (SELECT m.comment_id, m.parent_id, m.author_id, m.author_name, m.body_html, m.points, m.created_at, s.item_no, s.title
 FROM comment_meta m JOIN story_meta s ON s.entity_id = m.entity_id WHERE m.live AND NOT s.dead ORDER BY m.created_at DESC LIMIT 31 OFFSET $1) x`

type newRow struct {
	ID         string    `json:"comment_id"`
	ParentID   string    `json:"parent_id"`
	AuthorID   *int64    `json:"author_id"`
	AuthorName string    `json:"author_name"`
	BodyHTML   string    `json:"body_html"`
	Points     int       `json:"points"`
	CreatedAt  time.Time `json:"created_at"`
	ItemNo     int64     `json:"item_no"`
	Title      string    `json:"title"`
}

// newcommentsPage lists the newest live comments of live stories.
func newcommentsPage(r *core.Registry) func(*core.Page) error {
	return func(p *core.Page) error {
		n := pageNo(p)
		viewer := p.Viewer()
		var got []newRow
		if err := core.QueryJSON(sqlNewest, &got, (n-1)*newPageSize); err != nil {
			c := content("newcomments", textBroken, "", []map[string]any{}, "")
			c["empty"] = false
			place(p, c)
			return nil
		}
		more := ""
		if len(got) > newPageSize {
			got, more = got[:newPageSize], pagedHref("/newcomments", n+1)
		}
		ids := make([]string, len(got))
		for i, g := range got {
			ids[i] = g.ID
		}
		vv := newVoteView(viewer, ids)
		next := pagedHref("/newcomments", n)
		rows := make([]map[string]any, 0, len(got))
		for _, g := range got {
			own := viewer != nil && g.AuthorID != nil && *g.AuthorID == viewer.ID
			rows = append(rows, row(r, rowIn{
				ID: g.ID, ParentID: g.ParentID, Author: g.AuthorName, AuthorID: g.AuthorID, Own: own,
				Body: g.BodyHTML, Points: g.Points, Created: g.CreatedAt, ItemNo: g.ItemNo, Title: g.Title,
			}, vv, next))
		}
		place(p, content("newcomments", core.NoticeText(p.Query("m")), "", rows, more))
		return nil
	}
}
