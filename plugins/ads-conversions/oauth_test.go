package main

import (
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

func settingsFor(t *testing.T, h *fakeHost) settings {
	t.Helper()
	h.configure(`"notify_email":"owner@example.com"`)
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func tokenJSON(access, refresh string, expiresIn int) fetched {
	body := fmt.Sprintf(`{"access_token":%q,"expires_in":%d`, access, expiresIn)
	if refresh != "" {
		body += fmt.Sprintf(`,"refresh_token":%q`, refresh)
	}
	return fetched{Status: 200, Body: body + `}`}
}

var errTestMail = errors.New("mail unavailable")

func TestSaveLoadTokensSealedAndSecretBound(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	want := tokenSet{Access: "acc-1", Refresh: "ref-1", Expires: testNow.Add(time.Hour)}
	if err := saveTokens(s, platGoogle, want); err != nil {
		t.Fatal(err)
	}
	stored, _ := h.kv["token:google"].(string)
	if stored == "" || strings.Contains(stored, "acc-1") || strings.Contains(stored, "ref-1") {
		t.Fatalf("stored value is not sealed: %q", stored)
	}
	got, err := loadTokens(s, platGoogle)
	if err != nil || got.Access != "acc-1" || got.Refresh != "ref-1" || !got.Expires.Equal(want.Expires) {
		t.Fatalf("loaded %+v, %v", got, err)
	}
	s.Google.ClientSecret = "changed"
	if _, err := loadTokens(s, platGoogle); err != errReconnect {
		t.Fatalf("changed secret: %v", err)
	}
	if _, err := loadTokens(s, platLinkedIn); err != errNotConnected {
		t.Fatalf("no token: %v", err)
	}
}

func TestAuthorizationURLs(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	g, _ := url.Parse(authorizationURL(s, platGoogle, "st", "ch"))
	q := g.Query()
	if g.Host != "accounts.google.com" || q.Get("access_type") != "offline" || q.Get("prompt") != "consent" ||
		q.Get("scope") != "https://www.googleapis.com/auth/datamanager" || q.Get("code_challenge") != "ch" || q.Get("state") != "st" ||
		q.Get("redirect_uri") != "https://example.com/p/ads-conversions/oauth/google/callback" {
		t.Fatalf("google url %s", g)
	}
	l, _ := url.Parse(authorizationURL(s, platLinkedIn, "st", ""))
	q = l.Query()
	if l.Host != "www.linkedin.com" || q.Get("scope") != "rw_conversions r_ads" || q.Get("code_challenge") != "" ||
		q.Get("redirect_uri") != "https://example.com/p/ads-conversions/oauth/linkedin/callback" {
		t.Fatalf("linkedin url %s", l)
	}
}

func TestExchangeCode(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	seen := stubHTTP(t, tokenJSON("acc", "ref", 3600), nil)
	tok, err := exchangeCode(s, platGoogle, "the-code", "the-verifier")
	if err != nil || tok.Access != "acc" || tok.Refresh != "ref" || !tok.Expires.Equal(testNow.Add(time.Hour)) {
		t.Fatalf("tokens %+v, %v", tok, err)
	}
	req := (*seen)[0]
	body, _ := url.ParseQuery(string(req.Body))
	if req.URL != "https://oauth2.googleapis.com/token" || body.Get("code") != "the-code" || body.Get("code_verifier") != "the-verifier" ||
		body.Get("grant_type") != "authorization_code" || body.Get("client_secret") != "gsecret" {
		t.Fatalf("request %s %s", req.URL, req.Body)
	}
	// Google without a refresh token is refused; LinkedIn without one is fine.
	stubHTTP(t, tokenJSON("acc", "", 3600), nil)
	if _, err := exchangeCode(s, platGoogle, "c", "v"); err == nil {
		t.Fatal("google without a refresh token must fail")
	}
	seen = stubHTTP(t, tokenJSON("acc", "", 5184000), nil)
	if tok, err := exchangeCode(s, platLinkedIn, "c", ""); err != nil || tok.Refresh != "" {
		t.Fatalf("linkedin %+v, %v", tok, err)
	}
	if (*seen)[0].URL != "https://www.linkedin.com/oauth/v2/accessToken" {
		t.Fatalf("linkedin endpoint %s", (*seen)[0].URL)
	}
}

func TestAccessTokenRefreshesWhenNearExpiry(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(time.Hour)})
	seen := stubHTTP(t, tokenJSON("new", "", 3600), nil)
	if tok, err := accessToken(s, platGoogle, false); err != nil || tok != "old" || len(*seen) != 0 {
		t.Fatalf("fresh token: %q %v, calls %d", tok, err, len(*seen))
	}
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(time.Minute)})
	if tok, err := accessToken(s, platGoogle, false); err != nil || tok != "new" || len(*seen) != 1 {
		t.Fatalf("near expiry: %q %v, calls %d", tok, err, len(*seen))
	}
	got, _ := loadTokens(s, platGoogle)
	if got.Refresh != "ref" || got.Access != "new" {
		t.Fatalf("saved %+v: the refresh token must be kept", got)
	}
	if tok, err := accessToken(s, platGoogle, true); err != nil || tok != "new" || len(*seen) != 2 {
		t.Fatalf("forced: %q %v, calls %d", tok, err, len(*seen))
	}
}

func TestAccessTokenInvalidGrantBreaksConnectionAndMailsOnce(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref-secret", Expires: testNow.Add(-time.Hour)})
	stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_grant","error_description":"Token has been revoked"}`}, nil)
	for i := 0; i < 3; i++ {
		if _, err := accessToken(s, platGoogle, false); err != errReconnect {
			t.Fatalf("run %d: %v", i, err)
		}
	}
	if !isBroken(platGoogle) {
		t.Fatal("status:google must say reconnect")
	}
	if n := len(h.callsOf("mail.send")); n != 1 {
		t.Fatalf("mails = %d, want exactly 1", n)
	}
	mail := h.callsOf("mail.send")[0].args
	if !strings.Contains(mail, "owner@example.com") || !strings.Contains(mail, "Google Ads") {
		t.Fatalf("mail %s", mail)
	}
	clearBroken(platGoogle)
	if isBroken(platGoogle) || h.kv["alerted:google"] != nil {
		t.Fatal("clearBroken must reset the state and the alert flag")
	}
}

func TestMailRefusedLeavesAlertFlagUnset(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	h.mailErr = errTestMail
	markBroken(s, platTikTok)
	if _, ok := h.kv["alerted:tiktok"]; ok {
		t.Fatal("flag set although the mail failed")
	}
	h.mailErr = nil
	markBroken(s, platTikTok)
	markBroken(s, platTikTok)
	if _, ok := h.kv["alerted:tiktok"]; !ok || len(h.callsOf("mail.send")) != 2 {
		t.Fatalf("flag %v, mails %d", h.kv["alerted:tiktok"], len(h.callsOf("mail.send")))
	}
	s.NotifyEmail = ""
	h.calls = nil
	markBroken(s, platLinkedIn)
	if len(h.callsOf("mail.send")) != 0 {
		t.Fatal("no notify_email = no mail")
	}
}

func TestLinkedInWithoutRefreshTokenUsedUntilExpiry(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platLinkedIn, tokenSet{Access: "li-acc", Expires: testNow.Add(time.Minute)})
	seen := stubHTTP(t, fetched{}, nil)
	if tok, err := accessToken(s, platLinkedIn, false); err != nil || tok != "li-acc" || len(*seen) != 0 {
		t.Fatalf("%q %v", tok, err)
	}
	// A 401 (force) with nothing to refresh with breaks the connection.
	if _, err := accessToken(s, platLinkedIn, true); err != errReconnect || !isBroken(platLinkedIn) {
		t.Fatalf("forced: %v broken %v", err, isBroken(platLinkedIn))
	}
}

func TestTransientTokenFailureDoesNotBreak(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(-time.Minute)})
	stubHTTP(t, fetched{Status: 503, Body: "down"}, nil)
	if _, err := accessToken(s, platGoogle, false); err == nil || err == errReconnect || isBroken(platGoogle) {
		t.Fatalf("err %v broken %v", err, isBroken(platGoogle))
	}
}

func TestOAuthCallsLeakNoTokensOrSecrets(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "acc-secret", Refresh: "ref-secret", Expires: testNow.Add(-time.Hour)})
	stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_grant","error_description":"ref-secret acc-secret"}`}, nil)
	_, _ = accessToken(s, platGoogle, false)
	revokeGoogle("acc-secret")
	scanned := 0
	for _, c := range h.calls {
		if c.fn != "log" && c.fn != "mail.send" {
			continue
		}
		scanned++
		for _, bad := range []string{"acc-secret", "ref-secret", "gsecret", "lsecret", "tok-secret"} {
			if strings.Contains(c.args, bad) {
				t.Fatalf("%s call leaks %q: %s", c.fn, bad, c.args)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("nothing was scanned")
	}
}
