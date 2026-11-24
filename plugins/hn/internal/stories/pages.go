package stories

import (
	"errors"
	"net/url"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

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

// feedPage fills hn_feed on the feed paths. It loads first and places last, so
// a host failure leaves the page as it was.
func feedPage(r *core.Registry, p *core.Page) error {
	st, v := core.LoadSettings(), p.Viewer()
	rows, more, first, heading, err := loadFeed(feedReq{Path: p.Ctx.Path, Q: p.Query, Viewer: v, PageSize: st.PageSize})
	if err != nil {
		return nil
	}
	back := url.Values{}
	for _, k := range []string{"p", "next", "day", "site", "id"} {
		if s := p.Query(k); s != "" {
			back.Set(k, s)
		}
	}
	next := p.Ctx.Path
	if len(back) > 0 {
		next += "?" + back.Encode()
	}
	views := make([]map[string]any, 0, len(rows))
	for i, s := range rows {
		views = append(views, rowView(r, v, s, first+i, next, st))
	}
	place(p, "hn_feed", map[string]any{
		"notice": core.NoticeText(p.Query("m")), "heading": heading, "rows": views, "more": more, "empty": len(rows) == 0,
	}, p.Ctx.Path != "/") // "/" only fills a block the site already has
	return nil
}

// submitPage fills hn_submit: the login hint, the new-story form or the edit form.
func submitPage(p *core.Page) error {
	c := map[string]any{"notice": core.NoticeText(p.Query("m"))}
	v := p.Viewer()
	switch {
	case v == nil:
		c["state"], c["login_href"] = "login", "/login?m=story-login"
	case p.Query("id") == "":
		c["state"], c["action"] = "new", "/p/hn/submit"
	default:
		no, ok := parseItemNo(p.Query("id"))
		var ref *storyRef
		if ok {
			var err error
			if ref, err = loadRef(no, v.ID); err != nil {
				return nil
			}
		}
		code := nMissing
		if ref != nil {
			code = canChange(v, ref, core.LoadSettings())
		}
		if code != "" {
			c["state"], c["reason"] = "denied", core.NoticeText(string(code))
			break
		}
		n := strconv.FormatInt(no, 10)
		c["state"], c["action"], c["delete_action"] = "edit", "/p/hn/story/"+n+"/edit", "/p/hn/story/"+n+"/delete"
		c["title"], c["url"], c["text"] = ref.Title, ref.URL, ref.Body
		c["can_delete"] = false
		if ref.CommentCount == 0 {
			has, err := hasComments(ref.EntityID)
			c["can_delete"] = err == nil && !has // fail closed
		}
	}
	place(p, "hn_submit", c, true)
	return nil
}

// itemRow is a story with its text.
type itemRow struct {
	storyRow
	Body string `json:"body"`
}

// loadItem reads one story with the viewer's vote; nil when there is none. A
// hidden story stays reachable by its link.
func loadItem(itemNo, viewerID int64) (*itemRow, error) {
	var it itemRow
	err := core.QueryJSON(`SELECT to_jsonb(x) FROM (
SELECT s.entity_id, s.item_no, s.author_id, u.username AS by, s.title, s.url, s.domain, s.kind, s.points, s.comment_count, s.dead, s.created_at, s.body, v.created_at AS voted_at
FROM story_meta s JOIN users u ON u.id = s.author_id LEFT JOIN story_votes v ON v.story_id = s.entity_id AND v.user_id = $2
WHERE s.item_no = $1) x`, &it, itemNo, viewerID)
	if errors.Is(err, core.ErrNoRows) {
		return nil, nil
	}
	return &it, err
}

// itemPage fills hn_item on /item/{n}.
func itemPage(r *core.Registry, p *core.Page) error {
	no, ok := parseItemNo(strings.TrimSuffix(strings.TrimPrefix(p.Ctx.Path, "/item/"), "/"))
	if !ok {
		return nil
	}
	v := p.Viewer()
	var viewerID int64
	if v != nil {
		viewerID = v.ID
	}
	it, err := loadItem(no, viewerID)
	if err != nil || it == nil {
		return nil
	}
	c := map[string]any{"notice": core.NoticeText(p.Query("m"))}
	if it.Dead && (v == nil || !(v.Showdead || v.ID == it.AuthorID)) {
		c["gone"] = true
	} else {
		c["row"] = rowView(r, v, it.storyRow, 0, itemPath(no), core.LoadSettings())
		paras := []string{}
		for _, s := range strings.Split(it.Body, "\n\n") {
			if s = strings.TrimSpace(s); s != "" {
				paras = append(paras, s)
			}
		}
		c["paras"] = paras
	}
	place(p, "hn_item", c, true)
	return nil
}
