package accounts

import (
	"errors"
	"net/url"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// reservedNames cannot be claimed: staff-like names and the first path
// segments of the plugin's pages. Compared case-insensitively.
var reservedNames = map[string]bool{}

func init() {
	for _, n := range strings.Fields(`admin administrator root mod moderator staff system null anonymous hn
		news newest front newcomments ask show jobs best submit threads user from favorites login search item p`) {
		reservedNames[n] = true
	}
}

func reserved(name string) bool { return reservedNames[core.UsernameLower(name)] }

// noticer lets a signup gate choose the notice shown on refusal.
type noticer interface{ Notice() core.Notice }

func claimHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		back := core.SafeNext(req)
		if back == "/" {
			back = "/login"
		}
		fail := func(n core.Notice) (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice(back, n), map[string]string{"error": core.NoticeText(string(n))}), nil
		}
		key := req.CommenterKey
		if key == "" {
			return fail(noticeSigninFirst)
		}
		name := req.Form().Get("username") // not trimmed: " ab" is refused
		if core.ValidUsername(name) != nil {
			return fail(noticeNameBad)
		}
		if reserved(name) {
			return fail(noticeNameReserved)
		}
		u, err := core.UserByKey(key)
		if err != nil {
			return fail(noticeTryAgain)
		}
		if u != nil {
			return fail(noticeNameHas)
		}
		if err := r.RunGates(core.GateSignup, &core.GateReq{Kind: core.GateSignup, Req: req, Target: core.UsernameLower(name), Claim: true}); err != nil {
			if req.WantsJSON() {
				return core.JSONError(403, "sign-up is not available"), nil
			}
			var n noticer
			if errors.As(err, &n) && core.NoticeText(string(n.Notice())) != "" {
				return fail(n.Notice())
			}
			return fail(noticeSignupRefused)
		}
		rows, err := core.Exec("INSERT INTO users (commenter_key, username, username_lower) VALUES ($1, $2, $3) ON CONFLICT DO NOTHING",
			key, name, core.UsernameLower(name))
		if err != nil {
			return fail(noticeTryAgain)
		}
		if rows == 0 {
			return fail(noticeNameTaken)
		}
		to := "/user?id=" + url.QueryEscape(name)
		return core.Respond(req, core.WithNotice(to, noticeWelcome), map[string]string{"username": name}), nil
	}
}
