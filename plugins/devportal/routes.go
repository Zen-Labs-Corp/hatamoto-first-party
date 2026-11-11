// Command devportal is the developer portal of hatamoto.dev: sign in with
// GitHub, accept the terms, create a free developer account and register one
// ed25519 signing key.
//
// The portal UI is a section on a themed CMS page, filled in by the
// render.page filter; its forms post to the public routes below, which answer
// 303 back to the page.
package main

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// config reads a manifest config value (set by host_wasip1.go, a fake in tests).
var config = func(string) (string, bool) { return "", false }

func init() {
	guest.OnHTTP(serve)
	guest.OnFilter(sdk.RenderPage, pageFilter)
	guest.OnCron("gc", gcCron)
	guest.OnCron("mail", mailCron)
	guest.OnCron("recheck", recheckCron)
	guest.OnCron("resign", resignCron)
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /auth/callback":
		return handleCallback(r), nil
	case "POST /account":
		return handleAccount(r), nil
	case "POST /key":
		return handleKey(r), nil
	case "POST /logout":
		return handleLogout(r), nil
	case "POST /submit":
		return handleSubmit(r), nil
	case "GET /terms":
		return handleTerms(), nil
	case "GET /registry.json":
		return handleRegistry(), nil
	case "GET /admin/accounts":
		return handleAdminList(r), nil
	case "POST /admin/accounts/{id}/suspend":
		return handleAdminStatus(r, true), nil
	case "POST /admin/accounts/{id}/unsuspend":
		return handleAdminStatus(r, false), nil
	case "GET /admin/submissions":
		return handleReviewList(r), nil
	case "POST /admin/submissions/{id}/approve":
		return handleApprove(r), nil
	case "POST /admin/submissions/{id}/reject":
		return handleReject(r), nil
	case "POST /admin/listings/{id}/revoke":
		return handleRevokeVersion(r), nil
	case "POST /admin/accounts/{id}/revoke-key":
		return handleRevokeKey(r), nil
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"}), nil
}

// reply answers with a JSON body.
func reply(code int, v any) sdk.HTTPResponse {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{Status: http.StatusInternalServerError}
	}
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"},
		Body:    string(b),
	}
}

// htmlPage answers with an HTML page.
func htmlPage(code int, body string) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store"},
		Body:    body,
	}
}

// redirect answers 303 to a same-site path, optionally setting cookies.
func redirect(loc string, cookies ...sdk.SetCookie) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:     http.StatusSeeOther,
		Headers:    map[string]string{"Location": loc, "Cache-Control": "no-store"},
		SetCookies: cookies,
	}
}

// first returns the first element or "".
func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
