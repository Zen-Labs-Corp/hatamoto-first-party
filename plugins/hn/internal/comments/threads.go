package comments

import (
	"net/url"
	"strconv"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const (
	// threadsPageSize is also the most comments.subtree calls one render makes.
	threadsPageSize = 15
	// sdkAuthorPage is the page size of comments.by_author.
	sdkAuthorPage = 50

	textNoUser = "No such user."
)

const sqlCommentStories = `SELECT coalesce(jsonb_object_agg(m.comment_id::text, jsonb_build_object('item_no', s.item_no, 'title', s.title, 'author_id', m.author_id)), '{}')
 FROM comment_meta m JOIN story_meta s ON s.entity_id = m.entity_id WHERE m.comment_id IN (SELECT jsonb_array_elements_text($1::jsonb)::uuid)`

// storyRef is the story a comment belongs to and the member who wrote it.
type storyRef struct {
	ItemNo   int64  `json:"item_no"`
	Title    string `json:"title"`
	AuthorID *int64 `json:"author_id"`
}

// storyRefs looks up the story and author of comments in one query. A host
// error gives an empty map (links and names are left out).
func storyRefs(ids []string) map[string]storyRef {
	out := map[string]storyRef{}
	if ids = validIDs(ids); len(ids) == 0 {
		return out
	}
	var got map[string]storyRef
	if err := core.QueryJSON(sqlCommentStories, &got, jsonList(ids)); err == nil && got != nil {
		out = got
	}
	return out
}

// validIDs keeps the uuids; host answers reach SQL only through this filter.
func validIDs(ids []string) []string {
	out := make([]string, 0, len(ids))
	for _, id := range ids {
		if validID(id) {
			out = append(out, id)
		}
	}
	return out
}

// subtree is comments.subtree for one comment (first page).
func subtree(id string) (sdk.CommentSubtree, error) {
	var st sdk.CommentSubtree
	err := core.Call(sdk.FnCommentsSubtree, sdk.CommentSubtreeArgs{CommentID: id, Page: 1}, &st)
	return st, err
}

// authorSlice returns page p (1-based, threadsPageSize comments) of a member's
// comments, newest first, and whether more follow. At most two comments.by_author calls.
func authorSlice(key string, p int) (items []sdk.CommentItem, more bool, err error) {
	offset := (p - 1) * threadsPageSize
	page, start := offset/sdkAuthorPage+1, offset%sdkAuthorPage
	call := func(n int) (sdk.CommentThread, error) {
		var th sdk.CommentThread
		err := core.Call(sdk.FnCommentsByAuthor, sdk.CommentsByAuthorArgs{CommenterKey: key, Page: n}, &th)
		return th, err
	}
	th, err := call(page)
	if err != nil {
		return nil, false, err
	}
	all, more := th.Items, th.HasMore
	if len(all) < start+threadsPageSize && more {
		next, err := call(page + 1)
		if err != nil {
			return nil, false, err
		}
		all, more = append(all, next.Items...), next.HasMore
	}
	if start >= len(all) {
		return nil, false, nil
	}
	end := start + threadsPageSize
	if len(all) > end {
		return all[start:end], true, nil
	}
	return all[start:], more, nil
}

// failure puts a block that only carries a notice.
func failure(p *core.Page, mode, text string) {
	c := content(mode, text, "", []map[string]any{}, "")
	c["empty"] = false
	place(p, c)
}

// failText words a host error for a page.
func failText(err error) string {
	if isUnavailable(err) {
		return textUnavailable
	}
	return textBroken
}

// threadsPage shows one member's comments, each with its direct replies.
func threadsPage(r *core.Registry) func(*core.Page) error {
	return func(p *core.Page) error {
		name := p.Query("id")
		if name == "" {
			failure(p, "threads", textNoUser)
			return nil
		}
		u, err := core.UserByName(name)
		if err != nil {
			failure(p, "threads", textBroken)
			return nil
		}
		if u == nil {
			failure(p, "threads", textNoUser)
			return nil
		}
		n := pageNo(p)
		viewer := p.Viewer()
		items, more, err := authorSlice(u.CommenterKey, n)
		if err != nil {
			failure(p, "threads", failText(err))
			return nil
		}

		replies := make([][]sdk.CommentItem, len(items))
		ids := make([]string, 0, len(items))
		fetch := true
		for i, it := range items {
			ids = append(ids, it.ID)
			if !fetch {
				continue
			}
			st, err := subtree(it.ID)
			if isUnavailable(err) {
				fetch = false
				continue
			}
			if err != nil {
				continue
			}
			for _, x := range st.Items {
				if x.ParentID == it.ID {
					replies[i] = append(replies[i], x)
					ids = append(ids, x.ID)
				}
			}
		}

		refs, vv := storyRefs(ids), newVoteView(viewer, validIDs(ids))
		base := "/threads?id=" + url.QueryEscape(u.Username)
		next, moreHref := base, ""
		if n > 1 {
			next = base + "&p=" + strconv.Itoa(n)
		}
		if more {
			moreHref = base + "&p=" + strconv.Itoa(n+1)
		}
		rows := make([]map[string]any, 0, len(items))
		for i, it := range items {
			in := rowIn{ID: it.ID, ParentID: it.ParentID, Author: u.Username, AuthorID: &u.ID, Own: viewer != nil && viewer.ID == u.ID,
				Dead: it.Status == "dead", Body: it.BodyHTML, Points: 1, Created: it.CreatedAt, ItemNo: refs[it.ID].ItemNo, Title: refs[it.ID].Title}
			out := row(r, in, vv, next)
			kids := []map[string]any{}
			for _, x := range replies[i] {
				ref := refs[x.ID]
				own := viewer != nil && x.CommenterKey != "" && x.CommenterKey == viewer.CommenterKey
				kids = append(kids, row(r, rowIn{ID: x.ID, ParentID: x.ParentID, Author: x.AuthorName, AuthorID: ref.AuthorID, Own: own,
					Dead: x.Status == "dead", Body: x.BodyHTML, Points: 1, Created: x.CreatedAt, ItemNo: ref.ItemNo, Title: ref.Title, Depth: 1}, vv, next))
			}
			out["replies"] = kids
			rows = append(rows, out)
		}
		place(p, content("threads", core.NoticeText(p.Query("m")), u.Username+"'s comments", rows, moreHref))
		return nil
	}
}
