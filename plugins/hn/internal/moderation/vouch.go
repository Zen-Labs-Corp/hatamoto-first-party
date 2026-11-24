package moderation

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const sqlVouchInsert = `INSERT INTO vouches (kind, target_id, user_id, created_at) VALUES ($1, $2::uuid, $3, $4::timestamptz) ON CONFLICT DO NOTHING`

// handleVouch is POST /vouch/{kind}/{id}: a member with enough karma brings back
// an item that flags killed.
func handleVouch(q *core.Request) (sdk.HTTPResponse, error) {
	back := backTo(q)
	if len(q.Body) > maxModBody {
		return core.JSONError(413, "request too large"), nil
	}
	u := q.Viewer()
	if u == nil {
		return signedOut(q)
	}
	t, err := resolve(q.PathValue("kind"), q.PathValue("id"))
	if err != nil {
		return refuse(q, back, nError)
	}
	if t == nil {
		return refuse(q, back, nMissing)
	}
	switch {
	case !t.Dead:
		return refuse(q, back, nVouchAlive)
	case t.Cause == causeOwner || t.Cause == causeBan:
		return refuse(q, back, nVouchLocked)
	case u.Karma < core.LoadSettings().VouchKarma:
		return refuse(q, back, nVouchKarma)
	case t.AuthorID == u.ID:
		return refuse(q, back, nVouchOwn)
	}
	var res struct{ Blocked bool }
	if err := core.QueryJSON(sqlBlocked, &res, u.ID); err == nil && res.Blocked {
		return refuse(q, back, nBlocked)
	}
	if _, err := core.Exec(sqlVouchInsert, t.Kind, t.ID, u.ID, nowArg()); err != nil {
		return refuse(q, back, nError)
	}
	if n := restore(t, u.Username); n != "" {
		return refuse(q, back, n)
	}
	writeLog(u.Username, "vouch", t.logTarget(), "")
	return core.Respond(q, core.WithNotice(back, nVouched), map[string]any{"vouched": true, "dead": false}), nil
}
