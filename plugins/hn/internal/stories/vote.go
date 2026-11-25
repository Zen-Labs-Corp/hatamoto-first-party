package stories

import (
	"errors"
	"time"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// unvoteWindow is how long after voting a vote can be taken back.
const unvoteWindow = time.Hour

// storyRef is a story_meta row with the viewer's vote on it.
type storyRef struct {
	EntityID     string     `json:"entity_id"`
	ItemNo       int64      `json:"item_no"`
	AuthorID     int64      `json:"author_id"`
	Kind         string     `json:"kind"`
	Dead         bool       `json:"dead"`
	Points       int        `json:"points"`
	CommentCount int        `json:"comment_count"`
	CreatedAt    time.Time  `json:"created_at"`
	Title        string     `json:"title"`
	URL          string     `json:"url"`
	Body         string     `json:"body"`
	VotedAt      *time.Time `json:"voted_at"`
	// VoteUncounted: the viewer's vote was stored without counting it (voting-ring check).
	VoteUncounted bool `json:"vote_uncounted"`
}

// loadRef reads a story by item number with viewerID's vote; nil when there is no such story.
func loadRef(itemNo, viewerID int64) (*storyRef, error) {
	var ref storyRef
	err := core.QueryJSON(`SELECT to_jsonb(x) FROM (
SELECT s.entity_id, s.item_no, s.author_id, s.kind, s.dead, s.points, s.comment_count, s.created_at, s.title, s.url, s.body, v.created_at AS voted_at, NOT COALESCE(v.counted, true) AS vote_uncounted
FROM story_meta s LEFT JOIN story_votes v ON v.story_id = s.entity_id AND v.user_id = $2
WHERE s.item_no = $1) x`, &ref, itemNo, viewerID)
	if errors.Is(err, core.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &ref, nil
}

// voteResult is the outcome of castVote: a refusal (Notice, with Text for JSON clients) or the new state.
type voteResult struct {
	Notice core.Notice
	Text   string
	Points int
	Voted  bool
}

func refused(n core.Notice) voteResult {
	return voteResult{Notice: n, Text: core.NoticeText(string(n))}
}

// castVote applies how ("up" or "un") for u on ref.
func castVote(r *core.Registry, q *core.Request, u *core.User, ref *storyRef, how string) voteResult {
	switch {
	case ref.Kind == "job":
		return refused(nVoteJob)
	case ref.AuthorID == u.ID:
		return refused(nVoteOwn)
	case ref.Dead:
		return refused(nVoteDead)
	}
	voted := ref.VotedAt != nil
	if how == "un" {
		if !voted {
			return voteResult{Points: ref.Points}
		}
		if core.Now().Sub(*ref.VotedAt) > unvoteWindow {
			return refused(nUnvoteLate)
		}
		if ref.VoteUncounted {
			// an uncounted vote never gave a point, score or karma: nothing to take back
			if _, err := core.Exec(`DELETE FROM story_votes WHERE story_id = $1::uuid AND user_id = $2 AND created_at > $3::timestamptz - interval '1 hour'`, ref.EntityID, u.ID, nowArg()); err != nil {
				return refused(nError)
			}
			return voteResult{Points: ref.Points}
		}
		return applyVote(ref, u, -1, `WITH v AS (DELETE FROM story_votes WHERE story_id = $1::uuid AND user_id = $2 AND counted AND created_at > $3::timestamptz - interval '1 hour' RETURNING story_id)
UPDATE story_meta SET points = points - 1, score = `+scoreSQL("points - 1", "$3")+` FROM v WHERE story_meta.entity_id = v.story_id`)
	}
	gr := &core.GateReq{Kind: core.GateVote, User: u, Req: q, Target: ref.EntityID}
	if err := r.RunGates(core.GateVote, gr); err != nil {
		return voteResult{Notice: noticeOf(err), Text: err.Error()}
	}
	if voted {
		return voteResult{Points: ref.Points, Voted: true}
	}
	if gr.Ring {
		// stored, shown as voted, but no points, score or karma
		n, err := core.Exec(`INSERT INTO story_votes (story_id, user_id, created_at, counted) VALUES ($1::uuid, $2, $3::timestamptz, false) ON CONFLICT DO NOTHING`, ref.EntityID, u.ID, nowArg())
		if err != nil {
			return refused(nError)
		}
		if n == 1 && gr.Stored != nil {
			gr.Stored(false)
		}
		return voteResult{Points: ref.Points, Voted: true}
	}
	res := applyVote(ref, u, 1, `WITH v AS (INSERT INTO story_votes (story_id, user_id, created_at) VALUES ($1::uuid, $2, $3::timestamptz) ON CONFLICT DO NOTHING RETURNING story_id)
UPDATE story_meta SET points = points + 1, score = `+scoreSQL("points + 1", "$3")+` FROM v WHERE story_meta.entity_id = v.story_id`)
	if res.Notice == "" && res.Points == ref.Points+1 && gr.Stored != nil {
		gr.Stored(true)
	}
	return res
}

// applyVote runs the one statement of a vote and, when it changed a row, moves the author's karma.
func applyVote(ref *storyRef, u *core.User, delta int, sql string) voteResult {
	n, err := core.Exec(sql, ref.EntityID, u.ID, nowArg())
	if err != nil {
		return refused(nError)
	}
	if n != 1 {
		return voteResult{Points: ref.Points, Voted: ref.VotedAt != nil}
	}
	_ = core.AddKarma(ref.AuthorID, delta)
	return voteResult{Points: ref.Points + delta, Voted: delta > 0}
}

// handleVote is POST /vote/story/{id}: how=up (default) or how=un.
func handleVote(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
	back := core.SafeNext(q)
	fail := func(n core.Notice) (sdk.HTTPResponse, error) {
		if q.WantsJSON() {
			return core.JSONError(403, core.NoticeText(string(n))), nil
		}
		return core.Respond(q, core.WithNotice(back, n), nil), nil
	}
	itemNo, ok := parseItemNo(q.PathValue("id"))
	if !ok {
		return fail(nMissing)
	}
	u := q.Viewer()
	if u == nil {
		if q.WantsJSON() {
			return core.JSONError(401, "login required"), nil
		}
		return core.Respond(q, core.WithNotice("/login", nLogin), nil), nil
	}
	how := q.Form().Get("how")
	if how == "" && len(q.Query["how"]) > 0 {
		how = q.Query["how"][0]
	}
	if how != "un" {
		how = "up"
	}
	ref, err := loadRef(itemNo, u.ID)
	if err != nil {
		return fail(nError)
	}
	if ref == nil {
		return fail(nMissing)
	}
	res := castVote(r, q, u, ref, how)
	if res.Notice != "" {
		if q.WantsJSON() {
			return core.JSONError(403, res.Text), nil
		}
		return core.Respond(q, core.WithNotice(back, res.Notice), nil), nil
	}
	return core.Respond(q, back, map[string]any{"points": res.Points, "voted": res.Voted}), nil
}
