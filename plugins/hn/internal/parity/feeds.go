package parity

import (
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// pubStory is a story as a signed-out visitor sees it. It carries no vote,
// no hidden state and nothing about the author but the username.
type pubStory struct {
	ItemNo       int64     `json:"item_no"`
	Kind         string    `json:"kind"`
	Title        string    `json:"title"`
	URL          string    `json:"url"`
	Body         string    `json:"body"`
	By           string    `json:"by"`
	Points       int       `json:"points"`
	CommentCount int       `json:"comment_count"`
	CreatedAt    time.Time `json:"created_at"`
}

// feedDefs are the six feeds of the site (the same filters and orders as the
// pages); the anonymous view never filters hidden stories and never shows dead ones.
var feedDefs = map[string]struct{ where, order string }{
	"news":   {"s.kind <> 'job'", "s.score DESC, s.created_at DESC"},
	"newest": {"s.kind <> 'job'", "s.item_no DESC"},
	"ask":    {"s.kind = 'ask'", "s.score DESC, s.created_at DESC"},
	"show":   {"s.kind = 'show'", "s.score DESC, s.created_at DESC"},
	"jobs":   {"s.kind = 'job'", "s.created_at DESC"},
	"best":   {"s.kind <> 'job' AND s.created_at > $3::timestamptz - interval '7 days'", "s.points DESC, s.created_at DESC"},
}

const pubColumns = "s.item_no, s.kind, s.title, s.url, s.body, u.username AS by, s.points, s.comment_count, s.created_at"

// pubSelect is the SQL of a feed: $1 limit, $2 offset and, for best only, $3 now.
func pubSelect(name string) string {
	d := feedDefs[name]
	return "SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.ord), '[]'::jsonb) FROM (SELECT row_number() OVER (ORDER BY " + d.order + ") AS ord, " + pubColumns +
		" FROM story_meta s JOIN users u ON u.id = s.author_id WHERE NOT s.dead AND " + d.where + " ORDER BY " + d.order + " LIMIT $1 OFFSET $2) r"
}

const sqlPubItem = "SELECT to_jsonb(x) FROM (SELECT " + pubColumns + " FROM story_meta s JOIN users u ON u.id = s.author_id WHERE s.item_no = $1 AND NOT s.dead) x"

// loadPub reads one page of a feed (at most pageSize stories); more says there
// is a next page and ok is false for an unknown feed name.
func loadPub(name string, page int) (rows []pubStory, more, ok bool, err error) {
	if _, ok = feedDefs[name]; !ok {
		return nil, false, false, nil
	}
	args := []any{pageSize + 1, (page - 1) * pageSize}
	if name == "best" {
		args = append(args, nowArg()) // a bind argument no statement uses would fail
	}
	if err = core.QueryJSON(pubSelect(name), &rows, args...); err != nil {
		return nil, false, true, err
	}
	if len(rows) > pageSize {
		rows, more = rows[:pageSize], page < maxPage
	}
	return rows, more, true, nil
}

// firstQuery is the first value of a query key, or "".
func firstQuery(q *core.Request, key string) string {
	if v := q.Query[key]; len(v) > 0 {
		return v[0]
	}
	return ""
}
