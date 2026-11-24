package stories

import (
	"strconv"
	"strings"

	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const maxBody = 64 << 10 // bytes of a posted form

// handleSubmit is POST /submit: title plus url and/or text. A refusal goes back
// to the form with a notice; a link posted in the last 30 days becomes an
// upvote on the existing story.
func handleSubmit(r *core.Registry, q *core.Request) (sdk.HTTPResponse, error) {
	const back = "/submit"
	refuse := func(n core.Notice) (sdk.HTTPResponse, error) {
		return core.Respond(q, core.WithNotice(back, n), map[string]any{"error": core.NoticeText(string(n))}), nil
	}
	if len(q.Body) > maxBody {
		return core.JSONError(413, "too large"), nil
	}
	u := q.Viewer()
	if u == nil {
		return core.Respond(q, core.WithNotice("/login", nLogin), nil), nil
	}
	f := q.Form()
	title, err := core.CleanTitle(f.Get("title"))
	if err != nil {
		return refuse(nTitle)
	}
	rawURL, text := strings.TrimSpace(f.Get("url")), strings.TrimSpace(f.Get("text"))
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
	}
	if st := core.LoadSettings(); st.SubmitPerHour > 0 {
		var n int
		err := core.QueryJSON("SELECT to_jsonb(count(*)) FROM story_meta WHERE author_id = $1 AND created_at > $2::timestamptz - interval '1 hour'", &n, u.ID, nowArg())
		if err != nil {
			return refuse(nError)
		}
		if n >= st.SubmitPerHour {
			return refuse(nRate)
		}
	}
	if err := r.RunGates(core.GateSubmit, &core.GateReq{Kind: core.GateSubmit, User: u, Req: q, Target: key, Domain: domain}); err != nil {
		if q.WantsJSON() {
			return core.JSONError(403, err.Error()), nil
		}
		return refuse(noticeOf(err))
	}
	if key != "" {
		var dupNo int64
		err := core.QueryJSON("SELECT to_jsonb(item_no) FROM story_meta WHERE url_key = $1 AND created_at > $2::timestamptz - interval '30 days' ORDER BY created_at DESC LIMIT 1", &dupNo, key, nowArg())
		if err == nil && dupNo > 0 {
			if ref, _ := loadRef(dupNo, u.ID); ref != nil {
				castVote(r, q, u, ref, "up") // own, job and dead stories refuse silently
			}
			return core.Respond(q, core.WithNotice("/item/"+strconv.FormatInt(dupNo, 10), nDup), map[string]any{"item_no": dupNo}), nil
		}
	}
	no, n := createStory(r, newStory{AuthorID: u.ID, Title: title, URL: canon, URLKey: key, Domain: siteOf(domain), Text: text, Kind: kindOf(title)})
	if n != "" {
		return refuse(n)
	}
	return core.Respond(q, "/newest", map[string]any{"item_no": no}), nil
}
