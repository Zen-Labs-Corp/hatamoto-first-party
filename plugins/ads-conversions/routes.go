package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

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
	case "GET /log":
		return getLog(r)
	case "POST /test":
		return postTest(r)
	case "POST /click":
		return postClick(r)
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

// connect begins the OAuth flow. A browser navigation (Accept: text/html)
// gets a page that sends it on; any other caller gets the consent page
// address as JSON (a plugin may not redirect to another host).
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
	if !strings.Contains(r.Headers["accept"], "text/html") {
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

// platformStatus is the admin page's card for one platform: switches, what is
// missing, the connection and the non-secret identifiers. Never a secret.
func platformStatus(s settings, p string) map[string]any {
	missing := s.missing(p)
	if missing == nil {
		missing = []string{}
	}
	out := map[string]any{
		"label": platformLabel[p], "enabled": s.enabled(p), "configured": len(missing) == 0, "missing": missing,
		"needs_reconnect": isBroken(s, p),
	}
	switch p {
	case platGoogle:
		out["customer_id"], out["login_customer_id"], out["conversion_action_id"] = s.Google.CustomerID, s.Google.LoginCustomerID, s.Google.ConversionActionID
	case platTikTok:
		out["pixel_code"], out["event"], out["test_event_code"] = s.TikTok.PixelCode, s.TikTok.Event, s.TikTok.TestEventCode
		out["token_set"], out["connected"] = s.TikTok.AccessToken != "", s.TikTok.AccessToken != ""
		return out
	case platLinkedIn:
		out["rule_id"], out["ad_account_id"] = s.LinkedIn.RuleID, s.LinkedIn.AdAccountID
	}
	if s.PublicURL != "" {
		out["redirect_uri"] = s.redirectURI(p)
	}
	t, err := loadTokens(s, p)
	out["connected"] = err == nil
	if err == errReconnect {
		out["needs_reconnect"] = true
	}
	if err == nil {
		out["has_refresh_token"] = t.Refresh != ""
		if !t.Expires.IsZero() {
			out["token_expires"] = t.Expires.UTC().Format(time.RFC3339)
		}
	}
	return out
}

// getLog returns the newest 200 delivery rows (optionally one platform) and
// the platform cards.
func getLog(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	where, args := "", []any{}
	if p := first(r.Query["platform"]); p != "" {
		if !slices.Contains(platforms, p) {
			return invalid("unknown platform")
		}
		where, args = "WHERE platform = $1", append(args, p)
	}
	var rows json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (
SELECT created_at, submission_id, form_slug, platform, status, reason, code, message
FROM log `+where+` ORDER BY created_at DESC, id DESC LIMIT 200) t`, &rows, args...)
	if err != nil {
		return fail(err)
	}
	cards := map[string]any{}
	for _, p := range platforms {
		cards[p] = platformStatus(s, p)
	}
	forms := s.Forms
	if forms == nil {
		forms = []string{}
	}
	return reply(http.StatusOK, map[string]any{"forms": forms, "platforms": cards, "rows": rows})
}

// testResult is what the Test button shows; Test never moves the queue.
func testResult(ok bool, msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusOK, map[string]any{"ok": ok, "message": msg})
}

// postTest checks one platform with a fixed example address and no visitor
// data, and logs a test row. Google validates only, TikTok sends a test event
// (needs the test event code), LinkedIn reads the conversion rule because it
// has no test mode.
func postTest(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var in struct {
		Platform string `json:"platform"`
	}
	if json.Unmarshal([]byte(r.Body), &in) != nil || !slices.Contains(platforms, in.Platform) {
		return invalid("choose google, tiktok or linkedin")
	}
	p := in.Platform
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	if m := s.missing(p); len(m) > 0 {
		return invalid("Fill in " + strings.Join(m, ", ") + " in Settings first.")
	}
	t := now()
	j := job{SubmissionID: fmt.Sprintf("test-%d", t.UnixMilli()), Form: "test", Platform: p, EmHash: sha("test@example.com"),
		EmGoogleHash: sha("test@example.com"), SubmittedAt: t}
	var o outcome
	okMsg := ""
	switch p {
	case platTikTok:
		if s.TikTok.TestEventCode == "" {
			return invalid("Set a Test event code in Settings first (Events Manager > Test events).")
		}
		o = execute(buildTikTok(s, j, true), classifyTikTok)
		okMsg = "Sent. See Events Manager > Test events."
	case platGoogle, platLinkedIn:
		if p == platLinkedIn && s.LinkedIn.AdAccountID == "" {
			return invalid("Fill in Ad account ID in Settings first.")
		}
		tok, err := accessToken(s, p, false)
		if err != nil {
			return invalid("Connect " + platformLabel[p] + " first.")
		}
		if p == platGoogle {
			o = execute(buildGoogle(s, j, tok, true), classifyGoogle)
			okMsg = "Google accepted the test request (validate only)."
		} else {
			o, okMsg = checkLinkedIn(s, tok)
		}
	}
	if o.Kind == kindSent {
		if p == platTikTok {
			clearTikTokBroken(s)
		}
		record(j.SubmissionID, "test", p, "test", "ok", o.Code, cut(okMsg, maxMessage))
		return testResult(true, okMsg)
	}
	msg := testFailure(p, o)
	record(j.SubmissionID, "test", p, "test", "failed", o.Code, msg)
	return testResult(false, msg)
}

// testFailure words a failed test for the owner without any request data.
func testFailure(p string, o outcome) string {
	switch {
	case o.Kind == kindAuth:
		return platformLabel[p] + " refused the credentials. Reconnect or check the access token."
	case o.Message != "":
		return platformLabel[p] + " said: " + o.Message
	case o.Reason == "network":
		return "Could not reach " + platformLabel[p] + " (" + o.Code + "). Check the plugin's Permissions."
	}
	return platformLabel[p] + " could not be reached or refused the request (code " + o.Code + ")."
}

// checkLinkedIn reads the conversion rule; the token, version and ids are
// right when it is found. Returns the outcome and the success message.
func checkLinkedIn(s settings, token string) (outcome, string) {
	f, o := send(buildLinkedInCheck(s, token))
	if f.Status == 0 || f.Truncated || o.Kind != kindSent {
		if f.Status != 0 && o.Kind != kindSent {
			o.Message = cut(apiErrorMessage([]byte(f.Body)), maxMessage)
		}
		return o, ""
	}
	var rule struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(f.Body), &rule)
	if rule.Name == "" {
		return o, "Conversion rule found."
	}
	return o, "Conversion rule found: " + cut(rule.Name, 100) + "."
}
