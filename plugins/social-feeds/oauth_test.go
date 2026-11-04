package main

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const igSettings = `{"instagram_app_id":"123","instagram_app_secret":"APPSECRET","public_url":"https://example.com/"}`

func send(t *testing.T, method, pattern string, query map[string][]string, headers map[string]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Path: pattern, Pattern: pattern, Query: query, Headers: headers})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func callback(t *testing.T, state, code string) sdk.HTTPResponse {
	q := map[string][]string{"code": {code}}
	if state != "" {
		q["state"] = []string{state}
	}
	return send(t, "GET", "/oauth/callback", q, nil)
}

func startFlow(t *testing.T, f *fake) string {
	t.Helper()
	res := send(t, "GET", "/connect/instagram", nil, nil)
	if res.Status != 200 {
		t.Fatalf("connect = %d %s", res.Status, res.Body)
	}
	var out struct {
		URL string `json:"authorization_url"`
	}
	_ = json.Unmarshal([]byte(res.Body), &out)
	u, err := url.Parse(out.URL)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get("state")
}

func exchangeRoutes(f *fake, short string) {
	f.on(shortTokenURL, 200, short)
	f.on(graphURL+"access_token", 200, `{"access_token":"LONGTOKEN","token_type":"bearer","expires_in":5184000}`)
	f.on(graphURL+"me", 200, `{"user_id":"1789","username":"shop"}`)
}

func TestConnectStoresStateWithTTLAndBuildsURL(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	res := send(t, "GET", "/connect/instagram", nil, nil)
	var out struct {
		URL string `json:"authorization_url"`
	}
	_ = json.Unmarshal([]byte(res.Body), &out)
	u, _ := url.Parse(out.URL)
	q := u.Query()
	state := q.Get("state")
	if res.Status != 200 || u.Host != "www.instagram.com" || q.Get("client_id") != "123" || q.Get("scope") != "instagram_business_basic" ||
		q.Get("redirect_uri") != "https://example.com/p/social-feeds/oauth/callback" || len(state) != 32 {
		t.Fatalf("status %d url %s", res.Status, out.URL)
	}
	if e, ok := f.kv["oauth:"+state]; !ok || e.TTL != 600 {
		t.Fatalf("state not stored with ttl 600: %+v", f.kv)
	}
	if strings.Contains(res.Body, "APPSECRET") {
		t.Fatal("secret in the response")
	}
	html := send(t, "GET", "/connect/instagram", nil, map[string]string{"accept": "text/html"})
	if !strings.Contains(html.Headers["Content-Type"], "text/html") || !strings.Contains(html.Body, "http-equiv=\"refresh\"") {
		t.Fatalf("html answer = %+v", html)
	}
}

func TestConnectNeedsSettings(t *testing.T) {
	f := useFake(t)
	res := send(t, "GET", "/connect/instagram", nil, nil)
	if res.Status != 409 || !strings.Contains(res.Body, "instagram_app_id") || !strings.Contains(res.Body, "public_url") {
		t.Fatalf("answer = %d %s", res.Status, res.Body)
	}
	if len(f.kv) != 0 || f.fetchCount() != 0 {
		t.Fatal("must do nothing without settings")
	}
}

func TestCallbackRejectsBadStateWithoutFetching(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	exchangeRoutes(f, `{"access_token":"SHORT","user_id":1789}`)
	state := startFlow(t, f)

	for name, st := range map[string]string{"missing": "", "wrong": "deadbeef", "too long": strings.Repeat("a", 65)} {
		if res := callback(t, st, "CODE"); res.Status != 400 {
			t.Fatalf("%s state: status %d", name, res.Status)
		}
	}
	if f.fetchCount() != 0 {
		t.Fatal("fetched on a bad state")
	}

	// A good state works once; reuse is refused without another fetch.
	if res := callback(t, state, "CODE"); res.Status != 302 {
		t.Fatalf("first use: %d %s", res.Status, res.Body)
	}
	calls := f.fetchCount()
	if res := callback(t, state, "CODE"); res.Status != 400 {
		t.Fatalf("reuse: %d", res.Status)
	}
	if f.fetchCount() != calls {
		t.Fatal("fetched on a reused state")
	}
}

func TestCallbackStoresLongLivedTokenAndRedirects(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	exchangeRoutes(f, `{"access_token":"SHORT","user_id":1789}`)
	state := startFlow(t, f)
	res := callback(t, state, "CODE#_")
	if res.Status != 302 || res.Headers["Location"] != "/admin/plugins/social-feeds" || !strings.HasPrefix(res.Headers["Location"], "/") || strings.HasPrefix(res.Headers["Location"], "//") {
		t.Fatalf("answer = %+v", res)
	}
	if res.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("headers = %v", res.Headers)
	}
	if _, ok := f.kv["oauth:"+state]; ok {
		t.Fatal("state survived")
	}
	tok, _ := loadToken()
	if tok == nil || tok.AccessToken != "LONGTOKEN" || tok.Username != "shop" || tok.UserID != "1789" ||
		!tok.ExpiresAt.Equal(f.clock.Add(5184000*time.Second)) {
		t.Fatalf("token = %+v", tok)
	}
	if f.fetchCount() != 3 {
		t.Fatalf("fetches = %d, want exchange, long-lived, account", f.fetchCount())
	}
	post := f.fetches[0]
	form, _ := url.ParseQuery(post.Body)
	if post.Method != "POST" || post.Headers["Content-Type"] != "application/x-www-form-urlencoded" || form.Get("code") != "CODE" ||
		form.Get("redirect_uri") != "https://example.com/p/social-feeds/oauth/callback" || form.Get("grant_type") != "authorization_code" {
		t.Fatalf("exchange request = %+v", post)
	}
	for _, l := range f.logs {
		if strings.Contains(l, "LONGTOKEN") || strings.Contains(l, "APPSECRET") {
			t.Fatalf("secret in log %q", l)
		}
	}
}

func TestCallbackAcceptsWrappedShortTokenAnswer(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	exchangeRoutes(f, `{"data":[{"access_token":"SHORT","user_id":1789,"permissions":"instagram_business_basic"}]}`)
	state := startFlow(t, f)
	if res := callback(t, state, "CODE"); res.Status != 302 {
		t.Fatalf("answer = %d %s", res.Status, res.Body)
	}
}

func TestCallbackExchangeFailureIsRedacted(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	f.on(shortTokenURL, 400, `{"error_type":"OAuthException","code":400,"error_message":"Invalid code APPSECRET client_secret=APPSECRET"}`)
	state := startFlow(t, f)
	res := callback(t, state, "CODE")
	if res.Status != 502 || strings.Contains(res.Body, "APPSECRET") || !strings.Contains(res.Body, "Invalid code") {
		t.Fatalf("answer = %d %s", res.Status, res.Body)
	}
	if tok, _ := loadToken(); tok != nil {
		t.Fatal("token stored after failure")
	}
	for _, l := range f.logs {
		if strings.Contains(l, "APPSECRET") {
			t.Fatalf("secret in log %q", l)
		}
	}
}

func TestCallbackErrorParameter(t *testing.T) {
	f := useFake(t)
	f.settings = igSettings
	state := startFlow(t, f)
	res := send(t, "GET", "/oauth/callback", map[string][]string{"state": {state}, "error": {"access_denied"}}, nil)
	if res.Status != 400 || f.fetchCount() != 0 {
		t.Fatalf("answer = %d fetches %d", res.Status, f.fetchCount())
	}
}

func TestTokenCronYoungTokenMakesNoCall(t *testing.T) {
	f := useFake(t)
	saveTokenAged(t, f, 2*time.Hour)
	runTokenRefresh()
	if f.fetchCount() != 0 {
		t.Fatal("fetched for a young token")
	}
}

func saveTokenAged(t *testing.T, f *fake, age time.Duration) {
	t.Helper()
	obtained := f.clock.Add(-age)
	if err := saveToken(igToken{AccessToken: "OLDTOKEN", UserID: "1789", Username: "shop", ObtainedAt: obtained, ExpiresAt: obtained.Add(60 * 24 * time.Hour)}); err != nil {
		t.Fatal(err)
	}
}

func TestTokenCronRefreshesDayOldToken(t *testing.T) {
	f := useFake(t)
	saveTokenAged(t, f, 25*time.Hour)
	f.on(graphURL+"refresh_access_token", 200, `{"access_token":"NEWTOKEN","token_type":"bearer","expires_in":5184000}`)
	runTokenRefresh()
	tok, _ := loadToken()
	if f.fetchCount() != 1 || tok == nil || tok.AccessToken != "NEWTOKEN" || tok.Username != "shop" || !tok.ObtainedAt.Equal(f.clock) {
		t.Fatalf("fetches %d token %+v", f.fetchCount(), tok)
	}
	if !strings.Contains(f.fetches[0].URL, "grant_type=ig_refresh_token") {
		t.Fatalf("url = %s", f.fetches[0].URL)
	}
}

func TestTokenCronFailureKeepsOldTokenAndHidesIt(t *testing.T) {
	f := useFake(t)
	saveTokenAged(t, f, 25*time.Hour)
	f.on(graphURL+"refresh_access_token", 400, `{"error":{"message":"Session expired for OLDTOKEN"}}`)
	runTokenRefresh()
	tok, _ := loadToken()
	if tok == nil || tok.AccessToken != "OLDTOKEN" {
		t.Fatalf("token = %+v", tok)
	}
	st, _ := getStatus("instagram")
	if st.Error == "" || strings.Contains(st.Error, "OLDTOKEN") {
		t.Fatalf("status = %+v", st)
	}
	for _, l := range f.logs {
		if strings.Contains(l, "OLDTOKEN") {
			t.Fatalf("token in log %q", l)
		}
	}
}

func TestTokenCronWithoutTokenDoesNothing(t *testing.T) {
	f := useFake(t)
	runTokenRefresh()
	if f.fetchCount() != 0 {
		t.Fatal("fetched without a token")
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	useFake(t)
	if res := send(t, "GET", "/nope", nil, nil); res.Status != 404 {
		t.Fatalf("status = %d", res.Status)
	}
}
