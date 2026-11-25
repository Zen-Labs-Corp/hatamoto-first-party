package accounts

import (
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// logoutHandler signs this browser out. It needs no viewer: it clears any sign-in cookie,
// the email-link one too. sign_out stands alone in the session answer.
func logoutHandler(req *core.Request) (sdk.HTTPResponse, error) {
	resp := core.Respond(req, "/", map[string]any{"ok": true})
	resp.CommenterSession = &sdk.CommenterSession{SignOut: true}
	return resp, nil
}

// logoutAllHandler revokes every browser of the member's account, then signs this one out.
func logoutAllHandler(req *core.Request) (sdk.HTTPResponse, error) {
	fail := func(to string, n core.Notice) (sdk.HTTPResponse, error) {
		return core.Respond(req, core.WithNotice(to, n), map[string]string{"error": core.NoticeText(string(n))}), nil
	}
	v := req.Viewer()
	if v == nil {
		return fail("/login", noticeSigninFirst)
	}
	l, err := loginByUser(v.ID)
	if err != nil || l == nil {
		return fail(userHref(v.Username), noticeTryAgain)
	}
	var out sdk.CommentsIdentityRevoked
	if err := core.Call(sdk.FnCommentsIdentityRevoke, sdk.CommentsIdentityArgs{Account: l.Account}, &out); err != nil {
		return fail(userHref(v.Username), noticeTryAgain) // not signed out: the member must know it failed
	}
	// out.Revoked false: core has no commenter for the account, nothing else to sign out.
	resp := core.Respond(req, "/", map[string]any{"ok": true})
	resp.CommenterSession = &sdk.CommenterSession{SignOut: true}
	return resp, nil
}
