package polish

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// viewerState is what the viewer may do, from the karma thresholds.
type viewerState struct {
	Karma    int  `json:"karma"`
	Flag     bool `json:"flag"`
	Downvote bool `json:"downvote"`
	Vouch    bool `json:"vouch"`
}

// commentState is the viewer's relation to one comment: voted is "up", "down" or "".
type commentState struct {
	Voted string `json:"voted"`
	Own   bool   `json:"own"`
	Dead  bool   `json:"dead"`
}

type storyState struct {
	Voted bool `json:"voted"`
}

// state is the whole response. It never carries the commenter key or the name.
type state struct {
	Viewer   viewerState             `json:"viewer"`
	Story    storyState              `json:"story"`
	Comments map[string]commentState `json:"comments"`
}

// sqlState answers one jsonb document: the story vote and at most 500 comment
// rows of the entity, oldest first.
const sqlState = `SELECT jsonb_build_object(
 'story', jsonb_build_object('voted', EXISTS (SELECT 1 FROM story_votes v WHERE v.story_id = $1::uuid AND v.user_id = $2::bigint)),
 'comments', COALESCE((SELECT jsonb_object_agg(c.comment_id, jsonb_build_object(
     'voted', CASE c.value WHEN 1 THEN 'up' WHEN -1 THEN 'down' ELSE '' END,
     'own', COALESCE(c.author_id = $2::bigint, false),
     'dead', NOT c.live))
   FROM (SELECT m.comment_id, m.author_id, m.live, cv.value
         FROM comment_meta m LEFT JOIN comment_votes cv ON cv.comment_id = m.comment_id AND cv.user_id = $2::bigint
         WHERE m.entity_id = $1::uuid ORDER BY m.created_at, m.comment_id LIMIT 500) c), '{}'::jsonb))`

// handleState answers the viewer's vote and permission state for one item page.
// Every failure (no key, unknown member, bad id, host error) answers {}.
func handleState(q *core.Request) (sdk.HTTPResponse, error) {
	id := strings.ToLower(q.PathValue("entity_id"))
	if !uuidRe.MatchString(id) {
		return stateJSON(nil), nil
	}
	u := q.Viewer()
	if u == nil {
		return stateJSON(nil), nil
	}
	var s state
	if err := core.QueryJSON(sqlState, &s, id, u.ID); err != nil {
		return stateJSON(nil), nil
	}
	set := core.LoadSettings()
	s.Viewer = viewerState{
		Karma:    u.Karma,
		Flag:     u.Karma >= set.FlagKarma,
		Downvote: u.Karma >= set.DownvoteKarma,
		Vouch:    u.Karma >= set.VouchKarma,
	}
	if s.Comments == nil {
		s.Comments = map[string]commentState{}
	}
	return stateJSON(&s), nil
}

// stateJSON answers 200 with s, or {} for nil or a marshal error.
func stateJSON(s *state) sdk.HTTPResponse {
	body := []byte("{}")
	if s != nil {
		if b, err := json.Marshal(s); err == nil {
			body = b
		}
	}
	return sdk.HTTPResponse{
		Status:  200,
		Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"},
		Body:    string(body),
	}
}
