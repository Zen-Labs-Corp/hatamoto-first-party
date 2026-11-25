package accounts

import (
	"errors"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// signupHandler creates a password account: username + password, no email. Cheap checks come
// first; nothing is hashed or sent to the host before the gate and the taken-name check.
func signupHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		fail := func(n core.Notice) (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice("/login", n), map[string]string{"error": core.NoticeText(string(n))}), nil
		}
		f := req.Form()
		name, pw := f.Get("username"), f.Get("password")
		if core.ValidUsername(name) != nil {
			return fail(noticeNameBad)
		}
		if reserved(name) {
			return fail(noticeNameReserved)
		}
		if !validPassword(pw) {
			return fail(noticePwBad)
		}
		if err := r.RunGates(core.GateSignup, &core.GateReq{Kind: core.GateSignup, Req: req, Target: core.UsernameLower(name)}); err != nil {
			if req.WantsJSON() {
				return core.JSONError(403, "sign-up is not available"), nil
			}
			var n noticer
			if errors.As(err, &n) && core.NoticeText(string(n.Notice())) != "" {
				return fail(n.Notice())
			}
			return fail(noticeSignupRefused)
		}
		u, err := core.UserByName(name)
		if err != nil {
			return fail(noticeTryAgain)
		}
		if u != nil {
			return fail(noticeNameTaken)
		}
		hash, err := hashPassword(pw)
		if err != nil {
			return fail(noticeTryAgain)
		}
		account, err := newAccountID()
		if err != nil {
			return fail(noticeTryAgain)
		}
		var id sdk.CommentsIdentityEnsured
		if err := core.Call(sdk.FnCommentsIdentityEnsure, sdk.CommentsIdentityArgs{Account: account}, &id); err != nil || id.CommenterKey == "" {
			return fail(noticeSignupRefused)
		}
		// The key is stored at once: core resolves a fresh key in comments.by_author and
		// comments.set_viewer only after its next scan, so neither is called here.
		ok, err := createAccount(id.CommenterKey, name, account, hash)
		if err != nil {
			return fail(noticeTryAgain)
		}
		if !ok {
			return fail(noticeNameTaken) // lost a race; the orphan commenter is harmless
		}
		resp := core.Respond(req, core.WithNotice(userHref(name), noticeWelcome), map[string]string{"username": name})
		resp.CommenterSession = &sdk.CommenterSession{Account: account} // never Link on signup
		return resp, nil
	}
}
