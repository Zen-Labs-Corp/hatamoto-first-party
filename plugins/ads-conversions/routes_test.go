package main

import (
	"encoding/json"
	"fmt"
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

// outcomeOf reduces a callback answer to its status and page text, and checks
// that it is a page (never a redirect).
func outcomeOf(t *testing.T, res sdk.HTTPResponse) string {
	t.Helper()
	if res.Headers["Location"] != "" || !strings.HasPrefix(res.Headers["Content-Type"], "text/html") {
		t.Fatalf("a callback must answer a page, got %d %v", res.Status, res.Headers)
	}
	return fmt.Sprintf("%d %s", res.Status, res.Body[strings.Index(res.Body, "<p>"):])
}

const unknownState = "0123456789abcdef0123456789abcdef"

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
	if got := outcomeOf(t, res); got != "200 <p>Google Ads is connected. Go back to the plugin page and reload it.</p>" {
		t.Fatal(got)
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
	if res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "c"), nil); !strings.Contains(outcomeOf(t, res), "is connected") {
		t.Fatalf("first use: %v", res.Body)
	}
	res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(state, "c"), nil)
	if !strings.Contains(outcomeOf(t, res), "400 <p>Connection failed: The link has expired") {
		t.Fatalf("replay: %v", res.Body)
	}
}

func TestCallbackRejectsWrongPlatformUnknownOversizedAndDenied(t *testing.T) {
	h := newHost(t)
	h.configure()
	stub := stubHTTP(t, tokenJSON("a", "r", 3600), nil)
	state := startFlow(t, h, platGoogle)
	res := route(t, "GET", "/oauth/{platform}/callback", platLinkedIn, callbackQuery(state, "c"), nil)
	if !strings.Contains(outcomeOf(t, res), "Connection failed: The link belongs to another platform") {
		t.Fatalf("wrong platform: %v", res.Body)
	}
	if _, ok := h.kv["oauth:"+state]; ok {
		t.Fatal("a wrong-platform attempt must still use the state up")
	}
	res = route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(unknownState, "c"), nil)
	if !strings.Contains(outcomeOf(t, res), "The link has expired") {
		t.Fatalf("unknown: %v", res.Body)
	}
	// A state of the wrong shape never reaches the store.
	h.calls = nil
	for _, bad := range []string{"nosuchstate", strings.ToUpper(unknownState), unknownState + "0", "../token:google"} {
		res = route(t, "GET", "/oauth/{platform}/callback", platGoogle, callbackQuery(bad, "c"), nil)
		if !strings.Contains(outcomeOf(t, res), "The link is not valid") {
			t.Fatalf("state %q: %v", bad, res.Body)
		}
	}
	if len(h.callsOf("kv.get")) != 0 {
		t.Fatal("a malformed state must not be looked up")
	}
	big := strings.Repeat("a", 513)
	for _, q := range []map[string][]string{callbackQuery(big, "c"), callbackQuery(startFlow(t, h, platGoogle), big), callbackQuery("", "c")} {
		if res := route(t, "GET", "/oauth/{platform}/callback", platGoogle, q, nil); !strings.Contains(outcomeOf(t, res), "The link is not valid") {
			t.Fatalf("oversized: %v", res.Body)
		}
	}
	denied := callbackQuery(startFlow(t, h, platGoogle), "")
	denied["error"] = []string{"access_denied provider text"}
	res = route(t, "GET", "/oauth/{platform}/callback", platGoogle, denied, nil)
	if got := outcomeOf(t, res); got != "400 <p>Connection failed: Access was not granted.</p>" {
		t.Fatalf("denied: %v", got)
	}
	if len(*stub) != 0 {
		t.Fatal("no code exchange expected for a rejected callback")
	}
	if res := route(t, "GET", "/oauth/{platform}/callback", platTikTok, callbackQuery(unknownState, "c"), nil); !strings.Contains(outcomeOf(t, res), "Connection failed") {
		t.Fatalf("tiktok callback: %v", res.Body)
	}
}

func TestCallbackExchangeFailureGivesShortCodeOnly(t *testing.T) {
	h := newHost(t)
	h.configure()
	state := startFlow(t, h, platLinkedIn)
	stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_request","error_description":"secret provider text lsecret"}`}, nil)
	res := route(t, "GET", "/oauth/{platform}/callback", platLinkedIn, callbackQuery(state, "c"), nil)
	if got := outcomeOf(t, res); !strings.HasPrefix(got, "400 <p>Connection failed: The platform did not accept") || strings.Contains(res.Body, "secret provider text") {
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

func post(t *testing.T, pattern, body string) sdk.HTTPResponse {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: "POST", Pattern: pattern, Body: body, User: &sdk.HTTPUser{ID: "u1", Role: "owner"}})
	if err != nil {
		t.Fatal(err)
	}
	return res
}

func TestLogReturnsRowsAndCardsWithoutSecrets(t *testing.T) {
	h := deliveryHost(t)
	h.configure(`"google_login_customer_id":"111-222-3333"`, `"tiktok_test_event_code":"TEST123"`, `"linkedin_ad_account_id":"512345"`)
	h.query["jsonb_agg(to_jsonb(t))"] = `[{"status":"sent","platform":"google","submission_id":"s","code":"200"}]`
	res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/log", Query: map[string][]string{"platform": {"google"}}})
	if err != nil || res.Status != 200 {
		t.Fatalf("%d %v", res.Status, err)
	}
	var out struct {
		Forms     []string                  `json:"forms"`
		Platforms map[string]map[string]any `json:"platforms"`
		Rows      []map[string]any          `json:"rows"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil || len(out.Rows) != 1 || len(out.Forms) != 1 {
		t.Fatalf("%v %s", err, res.Body)
	}
	g := out.Platforms["google"]
	if g["connected"] != true || g["customer_id"] != "1234567890" || g["login_customer_id"] != "1112223333" || g["conversion_action_id"] != "987" ||
		g["token_expires"] == nil || g["has_refresh_token"] != true || g["redirect_uri"] != "https://example.com/p/ads-conversions/oauth/google/callback" {
		t.Fatalf("google card %v", g)
	}
	if tt := out.Platforms["tiktok"]; tt["pixel_code"] != "CABC123" || tt["test_event_code"] != "TEST123" || tt["token_set"] != true {
		t.Fatalf("tiktok card %v", tt)
	}
	if l := out.Platforms["linkedin"]; l["rule_id"] != "5" || l["ad_account_id"] != "512345" {
		t.Fatalf("linkedin card %v", l)
	}
	for _, bad := range []string{"gsecret", "lsecret", "tok-secret", "g-access", "g-refresh", "l-access"} {
		if strings.Contains(res.Body, bad) {
			t.Fatalf("/log leaks %q", bad)
		}
	}
	q := h.callsOf("db.query")[len(h.callsOf("db.query"))-1]
	if !strings.Contains(sqlOf(q), "WHERE platform = $1") || argsOf(q)[0] != "google" || !strings.Contains(sqlOf(q), "LIMIT 200") {
		t.Fatalf("query %s %v", sqlOf(q), argsOf(q))
	}
	if res, _ := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/log", Query: map[string][]string{"platform": {"x'; drop"}}}); res.Status != 400 {
		t.Fatalf("bad platform filter: %d", res.Status)
	}
}

func TestLogCardsShowMissingAndReconnect(t *testing.T) {
	h := newHost(t)
	h.configure(`"google_customer_id":""`)
	h.kv["status:linkedin"] = "reconnect"
	h.query["jsonb_agg(to_jsonb(t))"] = `[]`
	res, _ := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/log"})
	if !strings.Contains(res.Body, `"missing":["Customer ID"]`) || !strings.Contains(res.Body, `"needs_reconnect":true`) {
		t.Fatal(res.Body)
	}
}

func TestTestRouteGoogleValidatesOnly(t *testing.T) {
	h := deliveryHost(t)
	seen := platformHTTP(t, map[string]fetched{"datamanager.googleapis.com": {Status: 200, Body: `{}`}})
	res := post(t, "/test", `{"platform":"google"}`)
	if res.Status != 200 || !strings.Contains(res.Body, `"ok":true`) {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	body := string((*seen)[0].Body)
	if !strings.Contains(body, `"validateOnly":true`) || !strings.Contains(body, sha("test@example.com")) || (*seen)[0].Headers["Authorization"] != "Bearer g-access" {
		t.Fatalf("request %s", body)
	}
	rows := logRows(h, "test")
	if len(rows) != 1 || argsOf(rows[0])[3] != "google" || argsOf(rows[0])[5] != "ok" {
		t.Fatalf("log %+v", rows)
	}
	// A refusal is reported, not hidden, and logged as a failed test.
	platformHTTP(t, map[string]fetched{"datamanager.googleapis.com": {Status: 403, Body: `{"error":{"message":"no access"}}`}})
	res = post(t, "/test", `{"platform":"google"}`)
	if !strings.Contains(res.Body, `"ok":false`) || !strings.Contains(res.Body, "no access") {
		t.Fatalf("%s", res.Body)
	}
}

func TestTestRouteTikTokNeedsTestCode(t *testing.T) {
	h := deliveryHost(t)
	if res := post(t, "/test", `{"platform":"tiktok"}`); res.Status != 400 || !strings.Contains(res.Body, "Test event code") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	h.configure(`"tiktok_test_event_code":"TEST123"`)
	seen := platformHTTP(t, map[string]fetched{"business-api.tiktok.com": {Status: 200, Body: `{"code":0,"message":"OK"}`}})
	res := post(t, "/test", `{"platform":"tiktok"}`)
	if !strings.Contains(res.Body, `"ok":true`) || !strings.Contains(string((*seen)[0].Body), `"test_event_code":"TEST123"`) {
		t.Fatalf("%s", res.Body)
	}
	if strings.Contains(res.Body, "tok-secret") {
		t.Fatal("token in response")
	}
}

func TestTestRouteLinkedInReadsTheRule(t *testing.T) {
	h := deliveryHost(t)
	if res := post(t, "/test", `{"platform":"linkedin"}`); res.Status != 400 || !strings.Contains(res.Body, "Ad account ID") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	h.configure(`"linkedin_ad_account_id":"512345"`)
	seen := platformHTTP(t, map[string]fetched{"api.linkedin.com": {Status: 200, Body: `{"name":"Lead form"}`}})
	res := post(t, "/test", `{"platform":"linkedin"}`)
	if !strings.Contains(res.Body, "Lead form") || (*seen)[0].Method != "GET" || !strings.Contains((*seen)[0].URL, "/rest/conversions/5?account=urn%3Ali%3AsponsoredAccount%3A512345") {
		t.Fatalf("%s %v", res.Body, *seen)
	}
	platformHTTP(t, map[string]fetched{"api.linkedin.com": {Status: 404, Body: `{"message":"no such rule"}`}})
	if res := post(t, "/test", `{"platform":"linkedin"}`); !strings.Contains(res.Body, `"ok":false`) || !strings.Contains(res.Body, "no such rule") {
		t.Fatalf("%s", res.Body)
	}
}

func TestTestRouteValidation(t *testing.T) {
	h := newHost(t)
	h.configure(`"google_customer_id":""`)
	for _, body := range []string{`{"platform":"meta"}`, `nope`, `{}`} {
		if res := post(t, "/test", body); res.Status != 400 {
			t.Fatalf("%s: %d", body, res.Status)
		}
	}
	if res := post(t, "/test", `{"platform":"google"}`); res.Status != 400 || !strings.Contains(res.Body, "Customer ID") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	// Configured but not connected.
	h.configure()
	if res := post(t, "/test", `{"platform":"google"}`); res.Status != 400 || !strings.Contains(res.Body, "Connect Google Ads first") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

func TestClickRouteIsServedThroughTheRouter(t *testing.T) {
	h := newHost(t)
	res, err := serve(sdk.HTTPRequest{Method: "POST", Pattern: "/click", Headers: map[string]string{"content-type": "application/json"},
		Body: `{"submission_id":"` + clickSID + `","ids":{"gclid":"G1"}}`})
	if err != nil || res.Status != 204 || len(h.execs("INSERT INTO clicks")) != 1 {
		t.Fatalf("%d %v", res.Status, err)
	}
}
