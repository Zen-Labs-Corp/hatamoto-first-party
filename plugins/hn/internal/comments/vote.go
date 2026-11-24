package comments

import (
	"errors"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const sqlVoteTarget = `SELECT to_jsonb(x) FROM (SELECT author_id, live, points FROM comment_meta WHERE comment_id = $1::uuid) x`
const sqlVoteMine = `SELECT to_jsonb(x) FROM (SELECT value, created_at FROM comment_votes WHERE comment_id = $1::uuid AND user_id = $2) x`
const sqlVoteInsert = `INSERT INTO comment_votes (comment_id, user_id, value) VALUES ($1::uuid, $2, $3) ON CONFLICT DO NOTHING`
const sqlVoteDelete = `DELETE FROM comment_votes WHERE comment_id = $1::uuid AND user_id = $2 AND value = $3`

// sqlPoints recounts a comment's points from its votes (self-healing) and marks it for the rank cron.
const sqlPoints = `UPDATE comment_meta SET points = 1 + coalesce((SELECT sum(value) FROM comment_votes WHERE comment_id = $1::uuid), 0), dirty = true WHERE comment_id = $1::uuid`

// unvoteWindow is how long a member may take a vote back.
const unvoteWindow = time.Hour

// noticer lets a vote gate choose the notice shown on refusal.
type noticer interface{ Notice() core.Notice }

type voteTarget struct {
	AuthorID *int64 `json:"author_id"`
	Live     bool   `json:"live"`
	Points   int    `json:"points"`
}

// voteHandler serves POST /vote/comment/{id}: how = up | down | un.
// Refusals are notices on a redirect (or {error} for JSON clients), never errors.
func voteHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		back := core.SafeNext(req)
		fail := func(n core.Notice) (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice(back, n), map[string]string{"error": core.NoticeText(string(n))}), nil
		}
		if req.CommenterKey == "" {
			return core.Respond(req, core.WithNotice("/login", nSignin), map[string]string{"error": core.NoticeText(string(nSignin))}), nil
		}
		viewer := req.Viewer()
		if viewer == nil {
			return core.Respond(req, core.WithNotice("/login", nName), map[string]string{"error": core.NoticeText(string(nName))}), nil
		}
		id := req.PathValue("id")
		if !validID(id) {
			return fail(nMissing)
		}
		how := req.Form().Get("how")
		if how == "" {
			how = "up"
		}
		if how != "up" && how != "down" && how != "un" {
			return fail(nBad)
		}

		var target voteTarget
		if err := core.QueryJSON(sqlVoteTarget, &target, id); err != nil {
			if errors.Is(err, core.ErrNoRows) {
				return fail(nMissing)
			}
			return fail(nError)
		}
		if !target.Live {
			return fail(nDead)
		}
		if target.AuthorID != nil && *target.AuthorID == viewer.ID {
			return fail(nOwn)
		}
		if how == "down" && viewer.Karma < core.LoadSettings().DownvoteKarma {
			return fail(nKarma)
		}
		if err := r.RunGates(core.GateVote, &core.GateReq{Kind: core.GateVote, User: viewer, Req: req, Target: id}); err != nil {
			if req.WantsJSON() {
				return core.JSONError(403, "vote refused"), nil
			}
			var n noticer
			if errors.As(err, &n) && core.NoticeText(string(n.Notice())) != "" {
				return fail(n.Notice())
			}
			return fail(nRefused)
		}

		var value int
		switch how {
		case "up":
			value = 1
		case "down":
			value = -1
		}
		if how == "un" {
			var mine struct {
				Value   int       `json:"value"`
				Created time.Time `json:"created_at"`
			}
			if err := core.QueryJSON(sqlVoteMine, &mine, id, viewer.ID); err != nil {
				if errors.Is(err, core.ErrNoRows) {
					return fail(nNoVote)
				}
				return fail(nError)
			}
			if core.Now().Sub(mine.Created) > unvoteWindow {
				return fail(nLate)
			}
			rows, err := core.Exec(sqlVoteDelete, id, viewer.ID, mine.Value)
			if err != nil {
				return fail(nError)
			}
			if rows == 0 {
				return fail(nNoVote)
			}
			value = -mine.Value
		} else {
			rows, err := core.Exec(sqlVoteInsert, id, viewer.ID, value)
			if err != nil {
				return fail(nError)
			}
			if rows == 0 {
				return fail(nVoted) // a vote of either sign exists: unvote first
			}
		}
		if _, err := core.Exec(sqlPoints, id); err != nil {
			return fail(nError)
		}
		if target.AuthorID != nil {
			_ = core.AddKarma(*target.AuthorID, value) // fail open: the vote itself stands
		}

		points := target.Points + value
		if err := core.QueryJSON(sqlVoteTarget, &target, id); err == nil {
			points = target.Points
		}
		return core.Respond(req, back, map[string]any{"points": points, "voted": how != "un"}), nil
	}
}
