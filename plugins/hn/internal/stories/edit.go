package stories

import (
	"encoding/json"
	"encoding/json/jsontext"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// canChange is "" when u may edit or delete ref: the author, inside the edit
// window (edit_window_minutes, 0 means never). Otherwise the notice to show.
func canChange(u *core.User, ref *storyRef, st core.Settings) core.Notice {
	if u == nil || ref.AuthorID != u.ID {
		return nNotAuthor
	}
	if st.EditWindowMinutes <= 0 || core.Now().Sub(ref.CreatedAt) > time.Duration(st.EditWindowMinutes)*time.Minute {
		return nWindowOver
	}
	return ""
}

// ownStory loads the story of a route {id} for a signed-in author who may
// still change it. On refusal it returns the response to send instead.
func ownStory(q *core.Request, back func(no int64) string) (*core.User, *storyRef, *sdk.HTTPResponse) {
	refuse := func(to string, n core.Notice) (*core.User, *storyRef, *sdk.HTTPResponse) {
		resp := core.Respond(q, core.WithNotice(to, n), map[string]any{"error": core.NoticeText(string(n))})
		return nil, nil, &resp
	}
	no, ok := parseItemNo(q.PathValue("id"))
	if !ok {
		return refuse("/newest", nMissing)
	}
	u := q.Viewer()
	if u == nil {
		resp := core.Respond(q, core.WithNotice("/login", nLogin), nil)
		return nil, nil, &resp
	}
	ref, err := loadRef(no, u.ID)
	if err != nil {
		return refuse(back(no), nError)
	}
	if ref == nil {
		return refuse("/newest", nMissing)
	}
	if n := canChange(u, ref, core.LoadSettings()); n != "" {
		return refuse(back(no), n)
	}
	return u, ref, nil
}

func itemPath(no int64) string { return "/item/" + strconv.FormatInt(no, 10) }

// handleEdit is POST /story/{id}/edit: title, url and text of the author's own story.
func handleEdit(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
	back := func(no int64) string { return "/submit?id=" + strconv.FormatInt(no, 10) }
	_, ref, resp := ownStory(q, back)
	if resp != nil {
		return *resp, nil
	}
	refuse := func(n core.Notice) (sdk.HTTPResponse, error) {
		return core.Respond(q, core.WithNotice(back(ref.ItemNo), n), map[string]any{"error": core.NoticeText(string(n))}), nil
	}
	f := q.Form()
	title, err := core.CleanTitle(f.Get("title"))
	if err != nil {
		return refuse(nTitle)
	}
	rawURL, text := strings.TrimSpace(f.Get("url")), strings.TrimSpace(f.Get("text"))
	if text == "" && f.Get("keep_text") == "1" {
		text = ref.Body
	}
	if rawURL == "" && text == "" {
		return refuse(nEmpty)
	}
	if !textOK(text) {
		return refuse(nTextLong)
	}
	var canon, key, domain string
	if rawURL != "" {
		if canon, key, domain, err = core.NormalizeURL(rawURL); err != nil {
			return refuse(nURL)
		}
		if _, oldKey, _, _ := core.NormalizeURL(ref.URL); oldKey != key {
			var dupNo int64
			err := core.QueryJSON("SELECT to_jsonb(item_no) FROM story_meta WHERE url_key = $1 AND created_at > $2::timestamptz - interval '30 days' AND entity_id <> $3::uuid ORDER BY created_at DESC LIMIT 1", &dupNo, key, nowArg(), ref.EntityID)
			if err == nil && dupNo > 0 {
				return core.Respond(q, core.WithNotice(itemPath(dupNo), nDup), map[string]any{"item_no": dupNo}), nil
			}
		}
	}
	kind := kindOf(title)
	if ref.Kind == "job" {
		kind = "job"
	}
	attrs := map[string]string{"kind": kind}
	if canon != "" {
		attrs["url"] = canon
	}
	if text != "" {
		attrs["text"] = text
	}
	raw, err := json.Marshal(attrs)
	if err != nil {
		return refuse(nError)
	}
	if err := core.Call(sdk.FnEntitiesUpdate, sdk.EntityWrite{ID: ref.EntityID, Name: title, Attributes: jsontext.Value(raw)}, nil); err != nil {
		return refuse(hostNotice(err))
	}
	if _, err := core.Exec("UPDATE story_meta SET title = $2, url = $3, url_key = $4, domain = $5, body = $6, kind = $7 WHERE entity_id = $1::uuid",
		ref.EntityID, title, canon, key, siteOf(domain), text, kind); err != nil {
		return refuse(nError) // the entity is already updated: accepted residual risk
	}
	r.Publish(core.StoryEvent{Kind: core.StorySaved, EntityID: ref.EntityID, ItemNo: ref.ItemNo, Title: title, Body: text})
	return core.Respond(q, core.WithNotice(itemPath(ref.ItemNo), nSaved), map[string]any{"item_no": ref.ItemNo}), nil
}

// handleDelete is POST /story/{id}/delete: the author's own story, inside the
// edit window and while it has no comments. Karma is not touched.
func handleDelete(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
	_, ref, resp := ownStory(q, itemPath)
	if resp != nil {
		return *resp, nil
	}
	refuse := func(n core.Notice) (sdk.HTTPResponse, error) {
		return core.Respond(q, core.WithNotice(itemPath(ref.ItemNo), n), map[string]any{"error": core.NoticeText(string(n))}), nil
	}
	if ref.CommentCount > 0 {
		return refuse(nHasComments)
	}
	if err := core.Call(sdk.FnEntitiesDelete, sdk.EntityDeleteArgs{ID: ref.EntityID}, nil); err != nil {
		return refuse(hostNotice(err))
	}
	// story_votes and hidden rows cascade with story_meta.
	if _, err := core.Exec("DELETE FROM story_meta WHERE entity_id = $1::uuid", ref.EntityID); err != nil {
		return refuse(nError)
	}
	dropNumber(ref.ItemNo)
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted, EntityID: ref.EntityID, ItemNo: ref.ItemNo, Title: ref.Title})
	return core.Respond(q, core.WithNotice("/newest", nDeleted), map[string]any{"deleted": true}), nil
}
