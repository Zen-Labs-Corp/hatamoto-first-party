package stories

import (
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// feedReq is one request for a list of stories.
type feedReq struct {
	Path     string
	Q        func(string) string
	Viewer   *core.User
	PageSize int
}

// feedQuery is the SQL of a feed, its arguments ($1 viewer id, $2 showdead,
// $3 limit, $4 offset, then the feed's own) and how to page it.
type feedQuery struct {
	SQL       string
	Args      []any
	FirstRank int
	Heading   string
	Empty     bool                       // the feed is known to be empty: run no query
	More      func(last storyRow) string // href of the next page
}

// storyRow is one row of a feed.
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

const (
	scoreOrder = "s.score DESC, s.created_at DESC"
	pointOrder = "s.points DESC, s.created_at DESC"
	dateOrder  = "s.created_at DESC"
)

// feedSelect wraps a where and an order in the shared SELECT.
func feedSelect(where, order string, hidden bool) string {
	hide := ""
	if hidden {
		hide = " AND NOT EXISTS (SELECT 1 FROM hidden h WHERE h.story_id = s.entity_id AND h.user_id = $1)"
	}
	return "SELECT coalesce(jsonb_agg(to_jsonb(r) ORDER BY r.ord), '[]'::jsonb) FROM (SELECT row_number() OVER (ORDER BY " + order +
		") AS ord, s.entity_id, s.item_no, s.author_id, u.username AS by, s.title, s.url, s.domain, s.kind, s.points, s.comment_count, s.dead, s.created_at, v.created_at AS voted_at" +
		" FROM story_meta s JOIN users u ON u.id = s.author_id LEFT JOIN story_votes v ON v.story_id = s.entity_id AND v.user_id = $1" +
		" WHERE " + where + " AND ($2 OR NOT s.dead)" + hide + " ORDER BY " + order + " LIMIT $3 OFFSET $4) r"
}

// buildFeed turns a request into a feedQuery; ok is false for a path that is not a feed.
func buildFeed(f feedReq) (q feedQuery, ok bool) {
	var viewerID int64
	showdead := false
	if f.Viewer != nil {
		viewerID, showdead = f.Viewer.ID, f.Viewer.Showdead
	}
	page := parsePage(f.Q("p"))
	args := []any{viewerID, showdead, f.PageSize + 1, (page - 1) * f.PageSize}
	arg := func(v any) string {
		args = append(args, v)
		return "$" + strconv.Itoa(len(args))
	}
	where, order := "", scoreOrder
	pageHref := func(base string) func(storyRow) string {
		return func(storyRow) string { return base + "p=" + strconv.Itoa(page+1) }
	}
	more := pageHref("/news?")
	q.FirstRank = (page-1)*f.PageSize + 1

	feed := f.Path
	if feed == "/news" && f.Q("view") == "best" {
		feed = "best" // not a path: /best is no page path any more (16-path cap)
	}
	switch feed {
	case "/", "/news":
		where = "NOT s.dead AND s.kind <> 'job'"
		args[1] = false
	case "/newest":
		where, order = "s.kind <> 'job'", "s.item_no DESC"
		q.FirstRank, args[3] = 1, 0
		base := "/newest?"
		if id := strings.TrimSpace(f.Q("id")); id != "" {
			where += " AND u.username_lower = lower(" + arg(id) + ")"
			base += "id=" + url.QueryEscape(id) + "&"
			q.Heading = "Submissions by " + id
		}
		if n, err := strconv.ParseInt(f.Q("next"), 10, 64); err == nil && n > 0 {
			where += " AND s.item_no < " + arg(n)
		}
		more = func(last storyRow) string { return base + "next=" + strconv.FormatInt(last.ItemNo, 10) }
	case "/front":
		day, err := time.Parse("2006-01-02", f.Q("day"))
		base := "/front?"
		if err != nil {
			y := core.Now().UTC().AddDate(0, 0, -1)
			day = time.Date(y.Year(), y.Month(), y.Day(), 0, 0, 0, 0, time.UTC)
		} else {
			base += "day=" + day.Format("2006-01-02") + "&"
		}
		d := arg(day.Format(time.RFC3339))
		where = "s.kind <> 'job' AND s.created_at >= " + d + "::timestamptz AND s.created_at < " + d + "::timestamptz + interval '1 day'"
		order = pointOrder
		more = pageHref(base)
		q.Heading = "Front page submissions for " + day.Format("2006-01-02")
	case "/ask":
		where, more = "s.kind = 'ask'", pageHref("/ask?")
	case "/show":
		where, more = "s.kind = 'show'", pageHref("/show?")
	case "/jobs":
		where, order, more = "s.kind = 'job'", dateOrder, pageHref("/jobs?")
	case "best":
		where, order, more = "s.kind <> 'job' AND s.created_at > "+arg(nowArg())+"::timestamptz - interval '7 days'", pointOrder, pageHref("/news?view=best&")
	case "/from":
		site := siteOf(strings.TrimPrefix(strings.ToLower(strings.TrimSpace(f.Q("site"))), "www."))
		if site == "" {
			q.Empty = true
			return q, true
		}
		where, order, more = "s.domain = "+arg(site), dateOrder, pageHref("/from?site="+url.QueryEscape(site)+"&")
		q.Heading = "Stories from " + site
	default:
		return q, false
	}
	q.SQL, q.Args, q.More = feedSelect(where, order, true), args, more
	return q, true
}

// loadFeed runs the feed of f. more is the href of the next page, "" on the last.
func loadFeed(f feedReq) (rows []storyRow, more string, firstRank int, heading string, err error) {
	q, ok := buildFeed(f)
	if !ok {
		return nil, "", 1, "", nil
	}
	if q.Empty {
		return nil, "", 1, q.Heading, nil
	}
	if err = core.QueryJSON(q.SQL, &rows, q.Args...); err != nil {
		return nil, "", 1, "", err
	}
	if len(rows) > f.PageSize {
		rows = rows[:f.PageSize]
		more = q.More(rows[len(rows)-1])
	}
	return rows, more, q.FirstRank, q.Heading, nil
}

// rowView is the template map of one row; every value is a string, a bool or a
// slice. rank 0 leaves the rank empty (item head); next is the page to come back to.
func rowView(r *core.Registry, v *core.User, s storyRow, rank int, next string, st core.Settings) map[string]any {
	no := strconv.FormatInt(s.ItemNo, 10)
	itemHref := "/item/" + no
	voteHref := "/p/hn/vote/story/" + no
	rowNext := next + "#" + no
	m := map[string]any{
		"no": no, "title": s.Title, "href": itemHref, "external": s.URL != "", "site": s.Domain,
		"site_href": "/from?site=" + url.QueryEscape(s.Domain), "vote_href": voteHref, "next": rowNext,
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
	own := v != nil && v.ID == s.AuthorID
	switch {
	case s.Kind == "job" || own || s.Dead:
		m["vote"] = "none"
	case s.VotedAt != nil:
		m["vote"] = "voted"
	default:
		m["vote"] = "up"
	}

	var acts []map[string]any
	add := func(label, href, class string, post bool) {
		acts = append(acts, map[string]any{"label": label, "href": href, "class": class, "post": post, "next": ""})
	}
	if v != nil && s.VotedAt != nil && core.Now().Sub(*s.VotedAt) <= unvoteWindow {
		add("unvote", voteHref+"?how=un", "hn-unvote", true)
		acts[len(acts)-1]["next"] = rowNext
	}
	if v != nil && canChange(v, &storyRef{AuthorID: s.AuthorID, CreatedAt: s.CreatedAt}, st) == "" {
		add("edit", "/submit?id="+no, "hn-edit", false)
		add("delete", "/p/hn/story/"+no+"/delete", "hn-delete", true)
	}
	// past (a search for the title) follows hide, or ends the row without one.
	past := true
	for _, a := range r.ActionsFor(v, core.Item{Kind: "story", ID: s.EntityID, ItemNo: s.ItemNo, AuthorID: s.AuthorID, Dead: s.Dead}) {
		add(a.Label, a.Href, a.Class, a.Post)
		if past && (a.Class == "hn-hide" || a.Class == "hn-unhide") {
			add("past", "/search?q="+url.QueryEscape(s.Title), "hn-past", false)
			past = false
		}
	}
	if past {
		add("past", "/search?q="+url.QueryEscape(s.Title), "hn-past", false)
	}
	m["actions"] = acts
	return m
}
