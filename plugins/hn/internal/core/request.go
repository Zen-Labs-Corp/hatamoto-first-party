package core

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"hatamoto.dev/sdk"
)

// Request is an HTTP request to one of the plugin routes, with the form body
// and the viewer parsed lazily.
type Request struct {
	sdk.HTTPRequest
	viewer     *User
	viewerDone bool
	form       url.Values
	formDone   bool
}

// NewRequest wraps a host request.
func NewRequest(r sdk.HTTPRequest) *Request { return &Request{HTTPRequest: r} }

// Form parses the body as application/x-www-form-urlencoded once. A body that
// does not parse reads as empty.
func (r *Request) Form() url.Values {
	if !r.formDone {
		r.formDone = true
		v, err := url.ParseQuery(r.Body)
		if err != nil {
			v = url.Values{}
		}
		r.form = v
	}
	return r.form
}

// PathValue returns a wildcard of the matched route pattern.
func (r *Request) PathValue(k string) string { return r.PathValues[k] }

// Viewer is the member behind the request's commenter key, or nil (signed out,
// no username yet, or a host error: fail open).
func (r *Request) Viewer() *User {
	if !r.viewerDone {
		r.viewerDone = true
		if u, err := UserByKey(r.CommenterKey); err == nil {
			r.viewer = u
		}
	}
	return r.viewer
}

// WantsJSON is true when the client asks for application/json.
func (r *Request) WantsJSON() bool {
	return strings.Contains(r.Headers["accept"], "application/json")
}

// SameSite reports whether a browser-posted request may be accepted: the
// Sec-Fetch-Site header is absent, same-origin or none. Cross-site and
// same-site (another subdomain) are refused.
func SameSite(r *Request) bool {
	switch r.Headers["sec-fetch-site"] {
	case "", "same-origin", "none":
		return true
	}
	return false
}

// SafePath reports whether p is a same-site absolute path: one leading "/", no
// "//" or "/\", no byte at or below 0x20 or 0x7f (browsers strip tabs and
// newlines before resolving, so "/\t/evil" would become "//evil"), at most 2048
// bytes, and it parses as a URL with no scheme, host or opaque part. It mirrors
// the host's own redirect check.
func SafePath(p string) bool {
	if len(p) == 0 || len(p) > 2048 || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] <= 0x20 || p[i] == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.Opaque == ""
}

// SafeNext is where a form post goes back to: the form's "next" when it is a
// safe path, else the Referer's path (and query) when the browser says the post
// is same-origin, else "/".
func SafeNext(r *Request) string {
	if n := r.Form().Get("next"); SafePath(n) {
		return n
	}
	if r.Headers["sec-fetch-site"] == "same-origin" {
		if u, err := url.Parse(r.Headers["referer"]); err == nil {
			p := u.EscapedPath()
			if u.RawQuery != "" {
				p += "?" + u.RawQuery
			}
			if SafePath(p) {
				return p
			}
		}
	}
	return "/"
}

// Notice is a short code passed back as ?m=<code> on the redirect target.
type Notice string

var notices = map[string]string{}

// DefineNotice registers the text of a notice code; phases call it from Register.
// A duplicate code panics.
func DefineNotice(code Notice, text string) {
	if _, dup := notices[string(code)]; dup {
		panic(fmt.Sprintf("core: duplicate notice %q", code))
	}
	notices[string(code)] = text
}

// NoticeText is the text of a code, or "" for an unknown one (never echo the raw code).
func NoticeText(code string) string { return notices[code] }

// WithNotice appends m=<code> to a path; an empty notice leaves it unchanged.
func WithNotice(path string, n Notice) string {
	if n == "" {
		return path
	}
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	return path + sep + "m=" + url.QueryEscape(string(n))
}

// Respond answers a form post: the payload as JSON with 200 when the client
// asked for JSON, else a 303 to redirectTo (a safe path, else "/").
func Respond(r *Request, redirectTo string, payload any) sdk.HTTPResponse {
	if r.WantsJSON() {
		body, err := json.Marshal(payload)
		if err != nil {
			return JSONError(500, "internal")
		}
		return sdk.HTTPResponse{Status: 200, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: string(body)}
	}
	if !SafePath(redirectTo) {
		redirectTo = "/"
	}
	return sdk.HTTPResponse{Status: 303, Headers: map[string]string{"Location": redirectTo, "Cache-Control": "no-store"}}
}

// JSONError is {"error": msg} with a status and no-store.
func JSONError(status int, msg string) sdk.HTTPResponse {
	body, _ := json.Marshal(map[string]string{"error": msg})
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json", "Cache-Control": "no-store"}, Body: string(body)}
}
