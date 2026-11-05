package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func route(t *testing.T, method, pattern, platform string, query map[string][]string, headers map[string]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: map[string]string{"platform": platform},
		Query: query, Headers: headers, User: &sdk.HTTPUser{ID: "u1", Role: "owner"}})
	var req sdk.HTTPRequest
	_ = json.Unmarshal(in, &req)
	res, err := serve(req)
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func asJSON() map[string]string { return map[string]string{"accept": "application/json"} }

// startFlow runs connect and returns the state it stored.
func startFlow(t *testing.T, h *fakeHost, platform string) string {
	t.Helper()
	res := route(t, "GET", "/connect/{platform}", platform, nil, asJSON())
	var out struct {
		URL string `json:"authorization_url"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil || res.Status != 200 {
		t.Fatalf("connect: %d %s", res.Status, res.Body)
	}
	u, _ := url.Parse(out.URL)
	return u.Query().Get("state")
}

func location(res sdk.HTTPResponse) string { return res.Headers["Location"] }

func TestConnectJSONAndHTML(t *testing.T) {
	h := newHost(t)
	h.configure()
	state := startFlow(t, h, platGoogle)
	if len(state) != 32 {
		t.Fatalf("state %q", state)
	}
	set := h.callsOf("kv.set")[0].args
	if !strings.Contains(set, `"oauth:`+state+`"`) || !strings.Contains(set, `"ttl_seconds":600`) ||
		!strings.Contains(set, `"platform":"google"`) || !strings.Contains(set, `"user_id":"u1"`) || !strings.Contains(set, `"verifier":"`) {
		t.Fatalf("kv.set %s", set)
	}
	html := route(t, "GET", "/connect/{platform}", platLinkedIn, nil, map[string]string{"accept": "text/html"})
	if html.Status != 200 || !strings.Contains(html.Body, `http-equiv="refresh"`) || !strings.Contains(html.Body, "https://www.linkedin.com/oauth/v2/authorization?") ||
		html.Headers["Location"] != "" {
		t.Fatalf("html: %d %s", html.Status, html.Body)
	}
}

func TestConnectNeedsSettingsAndOAuthPlatform(t *testing.T) {
	h := newHost(t)
	h.configure(`"public_url":""`, `"google_client_secret":""`)
	res := route(t, "GET", "/connect/{platform}", platGoogle, nil, asJSON())
	if res.Status != 400 || !strings.Contains(res.Body, "Site address") || !strings.Contains(res.Body, "Client secret") || len(h.callsOf("kv.set")) != 0 {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if res := route(t, "GET", "/connect/{platform}", platTikTok, nil, asJSON()); res.Status != 400 {
		t.Fatalf("tiktok connect: %d", res.Status)
	}
}

func callbackQuery(state, code string) map[string][]string {
	return map[string][]string{"state": {state}, "code": {code}}
}

func TestCallbackHappyPathStoresSealedTokensAndClearsBroken(t *testing.T) {
	h := newHost(t)
	h.configure()
	h.kv["status:google"] = "reconnect"
	h.kv["alerted:google"] = true
	state := startFlow(t, h, platGoogle)
	seen := stubHTTP(t, tokenJSON("acc-new", "ref-new", 3600), nil)
	res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "the-code"), nil)
	if res.Status != 302 || location(res) != "/admin/plugins/ads-conversions" {
		t.Fatalf("%d %v", res.Status, res.Headers)
	}
	if len(*seen) != 1 || !strings.Contains(string((*seen)[0].Body), "code=the-code") {
		t.Fatalf("exchange %v", *seen)
	}
	s, _ := loadSettings()
	if tok, err := loadTokens(s, platGoogle); err != nil || tok.Access != "acc-new" {
		t.Fatalf("tokens %+v %v", tok, err)
	}
	if _, ok := h.kv["status:google"]; ok || h.kv["alerted:google"] != nil {
		t.Fatal("connect must clear the broken state")
	}
	if _, ok := h.kv["oauth:"+state]; ok {
		t.Fatal("state not deleted")
	}
}

func TestCallbackStateIsSingleUse(t *testing.T) {
	h := newHost(t)
	h.configure()
	state := startFlow(t, h, platGoogle)
	stubHTTP(t, tokenJSON("a", "r", 3600), nil)
	if res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "c"), nil); location(res) != uiPath {
		t.Fatalf("first use: %v", res.Headers)
	}
	res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "c"), nil)
	if location(res) != uiPath+"?error=expired" {
		t.Fatalf("replay: %v", res.Headers)
	}
}

func TestCallbackRejectsWrongPlatformUnknownOversizedAndDenied(t *testing.T) {
	h := newHost(t)
	h.configure()
	stub := stubHTTP(t, tokenJSON("a", "r", 3600), nil)
	state := startFlow(t, h, platGoogle)
	res := route(t, "GET", "/oauth/{platform}/callback", platLinkedIn, callbackQuery(state, "c"), nil)
	if location(res) != uiPath+"?error=wrong_platform" {
		t.Fatalf("wrong platform: %v", res.Headers)
	}
	if _, ok := h.kv["oauth:"+state]; ok {
		t.Fatal("a wrong-platform attempt must still use the state up")
	}
	res = route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery("nosuchstate", "c"), nil)
	if location(res) != uiPath+"?error=expired" {
		t.Fatalf("unknown: %v", res.Headers)
	}
	big := strings.Repeat("a", 513)
	for _, q := range []map[string][]string{callbackQuery(big, "c"), callbackQuery(startFlow(t, h, platGoogle), big), callbackQuery("", "c")} {
		if res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, q, nil); !strings.HasPrefix(location(res), uiPath+"?error=invalid_link") {
			t.Fatalf("oversized: %v", res.Headers)
		}
	}
	denied := callbackQuery(startFlow(t, h, platGoogle), "")
	denied["error"] = []string{"access_denied provider text"}
	res = route(t, "GET", "/oauth/{platform}/callback", platGoogle, denied, nil)
	if location(res) != uiPath+"?error=denied" || strings.Contains(location(res), "provider") {
		t.Fatalf("denied: %v", res.Headers)
	}
	if len(*stub) != 0 {
		t.Fatal("no code exchange expected for a rejected callback")
	}
	if res := route(t, "GET", "/oauth/{platform}/callback", platTikTok, callbackQuery("s", "c"), nil); !strings.HasPrefix(location(res), uiPath+"?error=") {
		t.Fatalf("tiktok callback: %v", res.Headers)
	}
}

func TestCallbackExchangeFailureGivesShortCodeOnly(t *testing.T) {
	h := newHost(t)
	h.configure()
	state := startFlow(t, h, platLinkedIn)
	stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_request","error_description":"secret provider text lsecret"}`}, nil)
	res := route(t, "GET", "/oauth/{platform}/callback", platLinkedIn, callbackQuery(state, "c"), nil)
	if location(res) != uiPath+"?error=exchange_failed" || res.Body != "" {
		t.Fatalf("%d %v %q", res.Status, res.Headers, res.Body)
	}
	if _, ok := h.kv["token:linkedin"]; ok {
		t.Fatal("no token expected")
	}
}

func TestDisconnect(t *testing.T) {
	h := newHost(t)
	h.configure()
	s, _ := loadSettings()
	_ = saveTokens(s, platGoogle, tokenSet{Access: "acc", Refresh: "ref-revoke", Expires: testNow.Add(time.Hour)})
	h.kv["status:google"] = "reconnect"
	seen := stubHTTP(t, fetched{Status: 200}, nil)
	res := route(t, "POST", "/disconnect/{platform}", platGoogle, nil, nil)
	if res.Status != 200 || res.Body != `{"ok":true}` {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if _, ok := h.kv["token:google"]; ok {
		t.Fatal("token kept")
	}
	if _, ok := h.kv["status:google"]; ok {
		t.Fatal("status kept")
	}
	if len(*seen) != 1 || (*seen)[0].URL != "https://oauth2.googleapis.com/revoke" || !strings.Contains(string((*seen)[0].Body), "ref-revoke") {
		t.Fatalf("revoke %v", *seen)
	}
	if res := route(t, "POST", "/disconnect/{platform}", platTikTok, nil, nil); res.Status != 400 {
		t.Fatalf("tiktok: %d", res.Status)
	}
}

func TestRoutesLeakNoSecrets(t *testing.T) {
	h := newHost(t)
	h.configure()
	state := startFlow(t, h, platGoogle)
	stubHTTP(t, tokenJSON("acc-secret", "ref-secret", 3600), nil)
	res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "c"), nil)
	bodies := res.Body + res.Headers["Location"]
	for _, bad := range []string{"acc-secret", "ref-secret", "gsecret"} {
		if strings.Contains(bodies, bad) {
			t.Fatalf("response leaks %q", bad)
		}
		for _, c := range h.callsOf("log") {
			if strings.Contains(c.args, bad) {
				t.Fatalf("log leaks %q", bad)
			}
		}
	}
	if res := route(t, "GET", "/nope", "", nil, nil); res.Status != 404 {
		t.Fatalf("unknown route: %d", res.Status)
	}
}
