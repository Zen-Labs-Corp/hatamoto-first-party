package moderation

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	sqlFlagInsert = `INSERT INTO flags (kind, target_id, user_id, created_at) VALUES ($1, $2::uuid, $3, $4::timestamptz) ON CONFLICT DO NOTHING`
	sqlFlagCount  = `SELECT to_jsonb(x) FROM (SELECT count(*) AS n FROM flags WHERE kind = $1 AND target_id = $2::uuid) x`
)

// refuse answers a refused action: JSON clients get the text, HTML clients a
// redirect carrying the notice.
func refuse(q *core.Request, back string, n core.Notice) (sdk.HTTPResponse, error) {
	if q.WantsJSON() {
		return core.JSONError(403, core.NoticeText(string(n))), nil
	}
	return core.Respond(q, core.WithNotice(back, n), nil), nil
}

// signedOut answers a request without a member.
func signedOut(q *core.Request) (sdk.HTTPResponse, error) {
	if q.WantsJSON() {
		return core.JSONError(401, "login required"), nil
	}
	return core.Respond(q, core.WithNotice("/login", nLogin), nil), nil
}

// noticeOf is the notice code of a gate refusal.
func noticeOf(err error) core.Notice {
	if r, ok := err.(interface{ Notice() core.Notice }); ok && core.NoticeText(string(r.Notice())) != "" {
		return r.Notice()
	}
	return nError
}

// handleFlag is POST /flag/{kind}/{id}.
func handleFlag(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
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
	if err := r.RunGates(core.GateFlag, &core.GateReq{Kind: core.GateFlag, User: u, Req: q, Target: t.ID}); err != nil {
		return refuse(q, back, noticeOf(err))
	}
	switch {
	case t.AuthorID == u.ID:
		return refuse(q, back, nFlagOwn)
	case t.Dead:
		return refuse(q, back, nFlagDead)
	}
	n, err := core.Exec(sqlFlagInsert, t.Kind, t.ID, u.ID, nowArg())
	if err != nil {
		return refuse(q, back, nError)
	}
	if n == 0 {
		return refuse(q, back, nFlagAgain)
	}
	killed := false
	var count struct{ N int }
	if err := core.QueryJSON(sqlFlagCount, &count, t.Kind, t.ID); err == nil {
		limit := commentFlagsToDead
		if t.Kind == kindStory {
			limit = core.LoadSettings().FlagsToDead
		}
		if count.N >= limit {
			killed, _ = kill(t, causeFlags, causeFlags)
		}
	}
	return core.Respond(q, core.WithNotice(back, nFlagged), map[string]any{"flagged": true, "dead": killed}), nil
}
