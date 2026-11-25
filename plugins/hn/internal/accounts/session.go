package accounts

import (
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// sessionRoute wraps a route whose answer carries commenter_session. Core honours
// it only for Sec-Fetch-Site: same-origin and refuses a same-site sibling or any
// other value with 403 AFTER the handler ran, so the route refuses first: no hash,
// ensure, revoke or write happens for a request core would not sign in.
// Stricter than core.SameSite (which lets an absent header and "none" through).
func sessionRoute(h func(*core.Request) (sdk.HTTPResponse, error)) func(*core.Request) (sdk.HTTPResponse, error) {
	return func(req *core.Request) (sdk.HTTPResponse, error) {
		if req.Headers["sec-fetch-site"] != "same-origin" {
			return core.JSONError(403, "cross-origin request refused"), nil
		}
		return h(req)
	}
}
