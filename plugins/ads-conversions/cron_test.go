package main

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func runRefresh(t *testing.T) {
	t.Helper()
	if err := onRefresh(sdk.CronEvent{Name: "refresh"}); err != nil {
		t.Fatal(err)
	}
}

func TestRefreshGoogleForcesOneRefresh(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(time.Hour)})
	seen := stubHTTP(t, tokenJSON("new", "", 3600), nil)
	runRefresh(t)
	if len(*seen) != 1 || !strings.Contains(string((*seen)[0].Body), "grant_type=refresh_token") {
		t.Fatalf("requests %v", *seen)
	}
	if tok, _ := loadTokens(s, platGoogle); tok.Access != "new" {
		t.Fatalf("token %+v", tok)
	}
}

func TestRefreshGoogleRevokedGrantBreaksOnceAcrossRuns(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(time.Hour)})
	seen := stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_grant"}`}, nil)
	runRefresh(t)
	runRefresh(t)
	runRefresh(t)
	if !isBroken(platGoogle) || len(h.callsOf("mail.send")) != 1 || len(*seen) != 1 {
		t.Fatalf("broken %v, mails %d, token calls %d (a broken platform is left alone)", isBroken(platGoogle), len(h.callsOf("mail.send")), len(*seen))
	}
}

func TestRefreshSkipsDisabledUnconnectedAndTransientFailures(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	seen := stubHTTP(t, tokenJSON("x", "", 3600), nil)
	runRefresh(t) // nothing connected
	h.configure(`"google_enabled":false`, `"linkedin_enabled":false`)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "old", Refresh: "ref", Expires: testNow.Add(time.Hour)})
	runRefresh(t)
	if len(*seen) != 0 {
		t.Fatalf("requests %v", *seen)
	}
	h.configure()
	stubHTTP(t, fetched{Status: 503}, nil)
	runRefresh(t)
	if isBroken(platGoogle) || len(h.callsOf("mail.send")) != 0 {
		t.Fatal("a server error must not break the connection")
	}
}

func TestRefreshLinkedInByExpiryAndRefreshToken(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	seen := stubHTTP(t, tokenJSON("li-new", "li-ref2", 5184000), nil)
	// Plenty of time left: nothing happens.
	_ = saveTokens(s, platLinkedIn, tokenSet{Access: "li", Refresh: "li-ref", Expires: testNow.Add(30 * 24 * time.Hour)})
	runRefresh(t)
	if len(*seen) != 0 {
		t.Fatal("refreshed too early")
	}
	// Under 7 days with a refresh token: refreshed.
	_ = saveTokens(s, platLinkedIn, tokenSet{Access: "li", Refresh: "li-ref", Expires: testNow.Add(6 * 24 * time.Hour)})
	runRefresh(t)
	if tok, _ := loadTokens(s, platLinkedIn); len(*seen) != 1 || tok.Access != "li-new" || tok.Refresh != "li-ref2" {
		t.Fatalf("token %+v, calls %d", tok, len(*seen))
	}
}

func TestRefreshLinkedInWithoutRefreshTokenMailsOnce(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	seen := stubHTTP(t, fetched{}, nil)
	_ = saveTokens(s, platLinkedIn, tokenSet{Access: "li", Expires: testNow.Add(3 * 24 * time.Hour)})
	runRefresh(t)
	runRefresh(t)
	mails := h.callsOf("mail.send")
	if len(mails) != 1 || !strings.Contains(mails[0].args, "2026-11-08") || len(*seen) != 0 || isBroken(platLinkedIn) {
		t.Fatalf("mails %d, calls %d, broken %v", len(mails), len(*seen), isBroken(platLinkedIn))
	}
	// Once it has run out the connection is marked broken.
	now = func() time.Time { return testNow.Add(4 * 24 * time.Hour) }
	runRefresh(t)
	if !isBroken(platLinkedIn) {
		t.Fatal("expired token without refresh token must need a reconnect")
	}
}

func TestRefreshNeverErrorsAndLeaksNothing(t *testing.T) {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "acc-secret", Refresh: "ref-secret", Expires: testNow.Add(time.Hour)})
	stubHTTP(t, fetched{Status: 400, Body: `{"error":"invalid_grant","x":"acc-secret ref-secret"}`}, nil)
	runRefresh(t)
	for _, c := range h.calls {
		if c.fn != "log" && c.fn != "mail.send" {
			continue
		}
		for _, bad := range []string{"acc-secret", "ref-secret", "gsecret", "lsecret"} {
			if strings.Contains(c.args, bad) {
				t.Fatalf("%s leaks %q", c.fn, bad)
			}
		}
	}
	h.panicOn = "settings.get"
	runRefresh(t)
}
