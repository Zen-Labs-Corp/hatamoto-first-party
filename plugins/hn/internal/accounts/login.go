package accounts

import (
	"errors"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const sqlReattach = `UPDATE users SET commenter_key = $1 WHERE id = $2 AND commenter_key <> $1`

// reattach points the member's row at the commenter core keeps for the account (they differ
// after an uninstall and reinstall of the plugin). Best-effort: any error (rate_limited from
// the writes limiter shared with submits, a unique conflict, a host error) leaves the row as it was.
func reattach(l *login) {
	var id sdk.CommentsIdentityEnsured
	if core.Call(sdk.FnCommentsIdentityEnsure, sdk.CommentsIdentityArgs{Account: l.Account}, &id) != nil || id.CommenterKey == "" {
		return
	}
	_, _ = core.Exec(sqlReattach, id.CommenterKey, l.UserID)
}

// gateNotice is the notice a refusing gate asks for, or fallback.
func gateNotice(err error, fallback core.Notice) core.Notice {
	var nr noticer
	if errors.As(err, &nr) && core.NoticeText(string(nr.Notice())) != "" {
		return nr.Notice()
	}
	return fallback
}

// loginHandler checks username + password. Unknown user, wrong password, a member without a
// login and an invalid name all answer the same and run exactly one bcrypt compare.
func loginHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		f := req.Form()
		name, pw := f.Get("username"), f.Get("password")
		valid := core.ValidUsername(name) == nil
		target := ""
		if valid {
			target = core.UsernameLower(name)
		}
		if err := r.RunGates(core.GateLogin, &core.GateReq{Kind: core.GateLogin, Req: req, Target: target}); err != nil {
			if req.WantsJSON() {
				return core.JSONError(429, "too many login attempts"), nil
			}
			return core.Respond(req, core.WithNotice("/login", gateNotice(err, noticeTryAgain)), nil), nil
		}
		bad := func() (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice("/login", noticeBadLogin), map[string]string{"error": core.NoticeText(string(noticeBadLogin))}), nil
		}
		var l *login
		if valid {
			var err error
			if l, err = loginByName(name); err != nil {
				return core.Respond(req, core.WithNotice("/login", noticeTryAgain), map[string]string{"error": core.NoticeText(string(noticeTryAgain))}), nil
			}
		}
		hash := ""
		if l != nil {
			hash = l.PwHash
		}
		if !checkPassword(hash, pw) || l == nil {
			return bad()
		}
		if needsRehash(l.PwHash) {
			if h, err := hashPassword(pw); err == nil {
				_ = setHash(l.UserID, h)
			}
		}
		reattach(l)
		resp := core.Respond(req, core.SafeNext(req), map[string]any{"ok": true})
		resp.CommenterSession = &sdk.CommenterSession{Account: l.Account} // never Link on login
		return resp, nil
	}
}
