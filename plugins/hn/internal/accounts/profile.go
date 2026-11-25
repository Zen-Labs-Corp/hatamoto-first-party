package accounts

import (
	"net/url"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const maxAbout = 2000

// capAbout drops NUL bytes (Postgres refuses them) and invalid UTF-8, then
// cuts the text to maxAbout bytes on a rune boundary.
func capAbout(s string) string {
	s = strings.ToValidUTF8(strings.ReplaceAll(s, "\x00", ""), "")
	if len(s) <= maxAbout {
		return s
	}
	s = s[:maxAbout]
	for len(s) > 0 && !utf8.ValidString(s) {
		s = s[:len(s)-1]
	}
	return s
}

// clamp parses raw and limits it to [lo, hi]; an unparsable value keeps cur.
func clamp(raw string, cur, lo, hi int) int {
	n, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil {
		return cur
	}
	return min(max(n, lo), hi)
}

func profileHandler(req *core.Request) (sdk.HTTPResponse, error) {
	v := req.Viewer()
	if v == nil {
		return core.Respond(req, core.WithNotice("/login", noticeSigninFirst),
			map[string]string{"error": core.NoticeText(string(noticeSigninFirst))}), nil
	}
	f := req.Form()
	about := capAbout(f.Get("about"))
	showdead, noprocrast := f.Get("showdead") == "yes", f.Get("noprocrast") == "yes"
	maxvisit := clamp(f.Get("maxvisit"), v.Maxvisit, 1, 120)
	minaway := clamp(f.Get("minaway"), v.Minaway, 1, 1440)
	to := "/user?id=" + url.QueryEscape(v.Username)
	if _, err := core.Exec("UPDATE users SET about = $1, showdead = $2, noprocrast = $3, maxvisit = $4, minaway = $5 WHERE id = $6",
		about, showdead, noprocrast, maxvisit, minaway, v.ID); err != nil {
		return core.Respond(req, core.WithNotice(to, noticeTryAgain),
			map[string]string{"error": core.NoticeText(string(noticeTryAgain))}), nil
	}
	if f.Has("email") {
		if l, err := loginByUser(v.ID); err == nil && l != nil { // only password members have an email
			email, ok := cleanEmail(f.Get("email"))
			if !ok {
				return core.Respond(req, core.WithNotice(to, noticeEmailBad),
					map[string]string{"error": core.NoticeText(string(noticeEmailBad))}), nil
			}
			if err := setEmail(v.ID, email); err != nil {
				return core.Respond(req, core.WithNotice(to, noticeTryAgain),
					map[string]string{"error": core.NoticeText(string(noticeTryAgain))}), nil
			}
		}
	}
	if core.Call != nil {
		// The viewer's show-dead flag follows the profile; the host may not offer it yet, so any error is ignored.
		_ = core.Call(sdk.FnCommentsSetViewer, sdk.CommentViewerArgs{CommenterKey: v.CommenterKey, ShowDead: showdead}, nil)
	}
	return core.Respond(req, core.WithNotice(to, noticeProfileSaved), map[string]any{"saved": true}), nil
}
