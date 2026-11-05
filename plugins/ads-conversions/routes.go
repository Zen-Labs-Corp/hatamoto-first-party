package main

import (
	"encoding/json"
	"html"
	"net/http"
	"net/url"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	// uiPath is the plugin's admin page, a same-site path as a redirect needs.
	uiPath = "/admin/plugins/ads-conversions"
	// stateTTL is how long a started OAuth flow stays valid, in seconds.
	stateTTL = 600
	// maxQueryValue caps the state and code a callback may carry.
	maxQueryValue = 512
)

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("refresh", onRefresh)
}

// serve answers the plugin's routes; which are public is decided in plugin.json.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	platform := r.PathValues["platform"]
	switch r.Method + " " + r.Pattern {
	case "GET /connect/{platform}":
		return connect(r, platform)
	case "GET /oauth/{platform}/callback":
		return callback(r, platform)
	case "POST /disconnect/{platform}":
		return disconnect(platform)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func invalid(msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusBadRequest, map[string]string{"error": msg})
}

// fail logs the host's error code and answers 500 without details.
func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "ads-conversions: request failed: %s", hostReason(err))
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

// oauthPlatform reports whether the platform connects through OAuth.
func oauthPlatform(p string) bool { return p == platGoogle || p == platLinkedIn }

// page is a small HTML document for the browser tabs of the OAuth flow (the
// host serves plugin HTML sandboxed, without scripts).
func page(status int, title, body string) sdk.HTTPResponse {
	doc := "<!doctype html><meta charset=utf-8><title>" + html.EscapeString(title) + "</title><body style=\"font:16px system-ui;margin:3em auto;max-width:34em\"><h1>" +
		html.EscapeString(title) + "</h1>" + body
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store"}, Body: doc}
}

// oauthState is what a started flow leaves in kv under oauth:<state>.
type oauthState struct {
	Platform string `json:"platform"`
	Verifier string `json:"verifier"`
	UserID   string `json:"user_id"`
}

// connectMissing lists what the OAuth start needs from the settings.
func connectMissing(s settings, platform string) []string {
	id, secret := clientCreds(s, platform)
	var m []string
	if s.PublicURL == "" {
		m = append(m, "Site address")
	}
	if id == "" {
		m = append(m, "Client ID")
	}
	if secret == "" {
		m = append(m, "Client secret")
	}
	return m
}

// connect begins the OAuth flow. Asked for JSON it returns the consent page
// address; navigated to it returns a page that sends the browser on (a plugin
// may not redirect to another host).
func connect(r sdk.HTTPRequest, platform string) (sdk.HTTPResponse, error) {
	if !oauthPlatform(platform) {
		return invalid("this platform does not use a connect flow")
	}
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	if m := connectMissing(s, platform); len(m) > 0 {
		return reply(http.StatusBadRequest, map[string]any{"error": "fill in the plugin settings first", "missing": m})
	}
	state := randomToken(16)
	var verifier, challenge string
	if platform == platGoogle {
		verifier, challenge = pkcePair()
	}
	uid := ""
	if r.User != nil {
		uid = r.User.ID
	}
	if err := kvSet("oauth:"+state, oauthState{Platform: platform, Verifier: verifier, UserID: uid}, stateTTL); err != nil {
		return fail(err)
	}
	target := authorizationURL(s, platform, state, challenge)
	if strings.Contains(r.Headers["accept"], "application/json") {
		return reply(http.StatusOK, map[string]string{"authorization_url": target})
	}
	link := html.EscapeString(target)
	return page(http.StatusOK, "Connecting to "+platformLabel[platform],
		`<meta http-equiv="refresh" content="0;url=`+link+`"><p><a href="`+link+`">Continue to `+platformLabel[platform]+`</a></p>`), nil
}

// back sends the browser to the admin page; code, when set, is a short error
// code (never provider text).
func back(code string) (sdk.HTTPResponse, error) {
	loc := uiPath
	if code != "" {
		loc += "?error=" + url.QueryEscape(code)
	}
	return sdk.HTTPResponse{Status: http.StatusFound, Headers: map[string]string{"Location": loc, "Cache-Control": "no-store"}}, nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// callback is where the platform sends the browser back. It is public, so the
// one-time state (random, 10 minutes, bound to the platform) is what proves
// this browser began the flow. Whatever happens, the state is used up.
func callback(r sdk.HTTPRequest, platform string) (sdk.HTTPResponse, error) {
	state, code := first(r.Query["state"]), first(r.Query["code"])
	if !oauthPlatform(platform) || state == "" || len(state) > maxQueryValue {
		return back("invalid_link")
	}
	var st oauthState
	found, err := kvGet("oauth:"+state, &st)
	_ = kvDelete("oauth:" + state)
	if err != nil {
		logf("warn", "ads-conversions: could not read the OAuth state: %s", hostReason(err))
		return back("internal")
	}
	switch {
	case !found:
		return back("expired")
	case st.Platform != platform:
		return back("wrong_platform")
	case first(r.Query["error"]) != "":
		return back("denied")
	case code == "" || len(code) > maxQueryValue:
		return back("invalid_link")
	}
	s, err := loadSettings()
	if err != nil {
		return back("internal")
	}
	tok, err := exchangeCode(s, platform, code, st.Verifier)
	if err != nil {
		logf("warn", "ads-conversions: %s code exchange failed", platform)
		return back("exchange_failed")
	}
	if err := saveTokens(s, platform, tok); err != nil {
		logf("warn", "ads-conversions: could not store the %s token", platform)
		return back("internal")
	}
	clearBroken(platform)
	logf("info", "ads-conversions: connected to %s", platformLabel[platform])
	return back("")
}

// disconnect forgets the stored tokens and the broken state; Google is asked
// to revoke the grant on a best-effort basis.
func disconnect(platform string) (sdk.HTTPResponse, error) {
	if !oauthPlatform(platform) {
		return invalid("this platform does not use a connect flow")
	}
	if s, err := loadSettings(); err == nil && platform == platGoogle {
		if t, err := loadTokens(s, platform); err == nil {
			if t.Refresh != "" {
				revokeGoogle(t.Refresh)
			} else {
				revokeGoogle(t.Access)
			}
		}
	}
	if err := kvDelete(tokenKeyPrefix + platform); err != nil {
		return fail(err)
	}
	clearBroken(platform)
	return reply(http.StatusOK, map[string]bool{"ok": true})
}
