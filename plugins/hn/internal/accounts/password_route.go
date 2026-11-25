package accounts

import (
	"strconv"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// passwordHead is the start both password routes share: a signed-in member with a username, the
// login gate (the current-password check is an oracle, and bcrypt is costly), a valid new password
// and the member's login row (nil when the member has none). A non-nil response ends the request.
func passwordHead(r *core.Registry, req *core.Request) (v *core.User, l *login, early *sdk.HTTPResponse) {
	fail := func(to string, n core.Notice) (*core.User, *login, *sdk.HTTPResponse) {
		resp := core.Respond(req, core.WithNotice(to, n), map[string]string{"error": core.NoticeText(string(n))})
		return nil, nil, &resp
	}
	v = req.Viewer()
	if v == nil {
		return fail("/login", noticeSigninFirst)
	}
	to := userHref(v.Username)
	if err := r.RunGates(core.GateMember, &core.GateReq{Kind: core.GateMember, User: v, Req: req, Target: strconv.FormatInt(v.ID, 10)}); err != nil {
		if req.WantsJSON() {
			resp := core.JSONError(429, "too many attempts")
			return nil, nil, &resp
		}
		return fail(to, gateNotice(err, noticeTryAgain))
	}
	if !validPassword(req.Form().Get("password")) {
		return fail(to, noticePwBad)
	}
	l, err := loginByUser(v.ID)
	if err != nil {
		return fail(to, noticeTryAgain)
	}
	return v, l, nil
}

// passwordHandler changes the password of a password member. It never links a commenter.
func passwordHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		v, l, early := passwordHead(r, req)
		if early != nil {
			return *early, nil
		}
		to := userHref(v.Username)
		fail := func(n core.Notice) (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice(to, n), map[string]string{"error": core.NoticeText(string(n))}), nil
		}
		if l == nil {
			return fail(noticePwNone)
		}
		if !checkPassword(l.PwHash, req.Form().Get("current")) {
			return fail(noticePwWrong)
		}
		hash, err := hashPassword(req.Form().Get("password"))
		if err != nil {
			return fail(noticeTryAgain)
		}
		if err := setHash(v.ID, hash); err != nil {
			return fail(noticeTryAgain)
		}
		// Other browsers out; the password is already changed, so an error or revoked=false is ignored.
		var out sdk.CommentsIdentityRevoked
		_ = core.Call(sdk.FnCommentsIdentityRevoke, sdk.CommentsIdentityArgs{Account: l.Account}, &out)
		resp := core.Respond(req, core.WithNotice(to, noticePwSaved), map[string]any{"saved": true})
		resp.CommenterSession = &sdk.CommenterSession{Account: l.Account} // this browser stays in; no Link
		return resp, nil
	}
}

// connectHandler is the explicit "connect a password" action of an email-link member, and the
// only route that sends Link: core binds this browser's commenter to the new account, so the
// member keeps username, comments and karma. A member who already has a login is refused.
func connectHandler(r *core.Registry) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		v, l, early := passwordHead(r, req)
		if early != nil {
			return *early, nil
		}
		to := userHref(v.Username)
		fail := func(n core.Notice) (sdk.HTTPResponse, error) {
			return core.Respond(req, core.WithNotice(to, n), map[string]string{"error": core.NoticeText(string(n))}), nil
		}
		if l != nil {
			return fail(noticePwHave)
		}
		hash, err := hashPassword(req.Form().Get("password"))
		if err != nil {
			return fail(noticeTryAgain)
		}
		account, err := newAccountID()
		if err != nil {
			return fail(noticeTryAgain)
		}
		if ok, err := addLogin(v.ID, account, hash); err != nil || !ok {
			return fail(noticeTryAgain)
		}
		// The viewer came from this request's commenter, so the commenter core binds is the member's own.
		resp := core.Respond(req, core.WithNotice(to, noticePwConnected), map[string]any{"connected": true})
		resp.CommenterSession = &sdk.CommenterSession{Account: account, Link: true}
		return resp, nil
	}
}
