package parity

import (
	"strconv"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Inserts resolve the story or comment inside the statement, so an unknown
// target is a no-op and never a foreign-key error.
const (
	sqlFavStory     = "INSERT INTO favorites (user_id, kind, target_id) SELECT $1, 'story', s.entity_id FROM story_meta s WHERE s.item_no = $2 ON CONFLICT DO NOTHING"
	sqlFavComment   = "INSERT INTO favorites (user_id, kind, target_id) SELECT $1, 'comment', c.comment_id FROM comment_meta c WHERE c.comment_id = $2::uuid AND c.live ON CONFLICT DO NOTHING"
	sqlUnfavStory   = "DELETE FROM favorites f USING story_meta s WHERE f.user_id = $1 AND f.kind = 'story' AND f.target_id = s.entity_id AND s.item_no = $2"
	sqlUnfavComment = "DELETE FROM favorites WHERE user_id = $1 AND kind = 'comment' AND target_id = $2::uuid"
	sqlHide         = "INSERT INTO hidden (user_id, story_id) SELECT $1, s.entity_id FROM story_meta s WHERE s.item_no = $2 ON CONFLICT DO NOTHING"
	sqlUnhide       = "DELETE FROM hidden h USING story_meta s WHERE h.user_id = $1 AND h.story_id = s.entity_id AND s.item_no = $2"
	sqlState        = "SELECT jsonb_build_object('fav', EXISTS (SELECT 1 FROM favorites f WHERE f.user_id = $1 AND f.kind = 'story' AND f.target_id = $2::uuid), 'hid', EXISTS (SELECT 1 FROM hidden h WHERE h.user_id = $1 AND h.story_id = $2::uuid))"
)

// refuse answers a refusal: a JSON error for a JSON client, else a redirect carrying the notice.
func refuse(q *core.Request, back string, status int, n core.Notice) sdk.HTTPResponse {
	if q.WantsJSON() {
		return core.JSONError(status, core.NoticeText(string(n)))
	}
	return core.Respond(q, core.WithNotice(back, n), nil)
}

// viewerOrLogin is the signed-in member with a username, or the refusal to send.
func viewerOrLogin(q *core.Request) (*core.User, *sdk.HTTPResponse) {
	if u := q.Viewer(); u != nil && u.Username != "" {
		return u, nil
	}
	var resp sdk.HTTPResponse
	if q.WantsJSON() {
		resp = core.JSONError(401, "login required")
	} else {
		resp = core.Respond(q, core.WithNotice("/login", nLogin), nil)
	}
	return nil, &resp
}

// handleFavorite is POST /favorite/{kind}/{id} (on) and /unfavorite/{kind}/{id}:
// kind is story (id = item number) or comment (id = comment uuid). Repeating
// either is a no-op.
func handleFavorite(q *core.Request, on bool) (sdk.HTTPResponse, error) {
	back := core.SafeNext(q)
	var sql string
	var target any
	switch q.PathValue("kind") {
	case "story":
		no, ok := parseItemNo(q.PathValue("id"))
		if !ok {
			return refuse(q, back, 404, nMissing), nil
		}
		sql, target = sqlUnfavStory, no
		if on {
			sql = sqlFavStory
		}
	case "comment":
		id := q.PathValue("id")
		if !isUUID(id) {
			return refuse(q, back, 404, nMissing), nil
		}
		sql, target = sqlUnfavComment, id
		if on {
			sql = sqlFavComment
		}
	default:
		return refuse(q, back, 404, nMissing), nil
	}
	u, resp := viewerOrLogin(q)
	if resp != nil {
		return *resp, nil
	}
	if _, err := core.Exec(sql, u.ID, target); err != nil {
		return refuse(q, back, 500, nError), nil
	}
	return core.Respond(q, back, map[string]any{"favorited": on}), nil
}

// handleHide is POST /hide/{id} (on) and /unhide/{id}: id is the item number.
func handleHide(q *core.Request, on bool) (sdk.HTTPResponse, error) {
	back := core.SafeNext(q)
	no, ok := parseItemNo(q.PathValue("id"))
	if !ok {
		return refuse(q, back, 404, nMissing), nil
	}
	u, resp := viewerOrLogin(q)
	if resp != nil {
		return *resp, nil
	}
	sql := sqlUnhide
	if on {
		sql = sqlHide
	}
	if _, err := core.Exec(sql, u.ID, no); err != nil {
		return refuse(q, back, 500, nError), nil
	}
	return core.Respond(q, back, map[string]any{"hidden": on}), nil
}

// rowActions offers hide and favorite on a story row to a member with a
// username. A host error offers nothing.
func rowActions(v *core.User, it core.Item) []core.Action {
	if v == nil || v.Username == "" || it.Kind != "story" || it.ItemNo <= 0 {
		return nil
	}
	var st struct {
		Fav bool `json:"fav"`
		Hid bool `json:"hid"`
	}
	if err := core.QueryJSON(sqlState, &st, v.ID, it.ID); err != nil {
		return nil
	}
	no := strconv.FormatInt(it.ItemNo, 10)
	hide := core.Action{Label: "hide", Href: "/p/hn/hide/" + no, Class: "hn-hide", Post: true}
	if st.Hid {
		hide = core.Action{Label: "unhide", Href: "/p/hn/unhide/" + no, Class: "hn-unhide", Post: true}
	}
	fav := core.Action{Label: "favorite", Href: "/p/hn/favorite/story/" + no, Class: "hn-fav", Post: true}
	if st.Fav {
		fav = core.Action{Label: "unfavorite", Href: "/p/hn/unfavorite/story/" + no, Class: "hn-unfav", Post: true}
	}
	return []core.Action{hide, fav}
}
