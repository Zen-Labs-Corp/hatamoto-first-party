package main

import (
	"fmt"
	"net/url"
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
	if !broken(platGoogle) || len(h.callsOf("mail.send")) != 1 || len(*seen) != 1 {
		t.Fatalf("broken %v, mails %d, token calls %d (a broken platform is left alone)", broken(platGoogle), len(h.callsOf("mail.send")), len(*seen))
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
	if broken(platGoogle) || len(h.callsOf("mail.send")) != 0 {
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
	if len(mails) != 1 || !strings.Contains(mails[0].args, "2026-11-08") || len(*seen) != 0 || broken(platLinkedIn) {
		t.Fatalf("mails %d, calls %d, broken %v", len(mails), len(*seen), broken(platLinkedIn))
	}
	// Once it has run out the connection is marked broken.
	now = func() time.Time { return testNow.Add(4 * 24 * time.Hour) }
	runRefresh(t)
	if !broken(platLinkedIn) {
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

// --- deliver and retry ---------------------------------------------------------------

func rowJSON(id int, platform string, attempts int, extra string) string {
	return fmt.Sprintf(`{"id":%d,"submission_id":"7f3c1d52-0000-4000-8000-00000000a91e","form_slug":"contact","platform":%q,
"em_hash":%q,"em_google_hash":%q,"ph_hash":%q,"page_path":"/thanks","consent":false,"submitted_at":"2026-11-05T09:59:00Z",
"attempts":%d,"created_at":"2026-11-05T09:58:00Z","ids_joined":false%s}`,
		id, platform, sha("jane.doe@example.com"), sha("janedoe@example.com"), sha("+491512345678"), attempts, extra)
}

func queueOf(rows ...string) string { return "[" + strings.Join(rows, ",") + "]" }

// platformHTTP stubs every platform call by host and records the requests.
func platformHTTP(t *testing.T, answers map[string]fetched) *[]request {
	t.Helper()
	var seen []request
	httpDo = func(req request, timeoutMS int) (fetched, error) {
		seen = append(seen, req)
		u, _ := url.Parse(req.URL)
		if f, ok := answers[u.Host]; ok {
			return f, nil
		}
		t.Fatalf("unexpected call to %s", req.URL)
		return fetched{}, nil
	}
	t.Cleanup(func() { httpDo = realHTTPDo })
	return &seen
}

// deliveryHost sets up a host with the three platforms connected and the given
// rows claimable.
func deliveryHost(t *testing.T, rows ...string) *fakeHost {
	h := newHost(t)
	s := settingsFor(t, h)
	_ = saveTokens(s, platGoogle, tokenSet{Access: "g-access", Refresh: "g-refresh", Expires: testNow.Add(time.Hour)})
	_ = saveTokens(s, platLinkedIn, tokenSet{Access: "l-access", Refresh: "l-refresh", Expires: testNow.Add(time.Hour)})
	h.query["FOR UPDATE SKIP LOCKED"] = queueOf(rows...)
	h.query["FROM clicks WHERE submission_id"] = `{}`
	return h
}

func runDeliver(t *testing.T) {
	t.Helper()
	if err := onDeliver(sdk.CronEvent{Name: "deliver"}); err != nil {
		t.Fatal(err)
	}
}

func deletes(h *fakeHost) []string {
	var ids []string
	for _, c := range h.execs("DELETE FROM queue WHERE id") {
		ids = append(ids, fmt.Sprint(argsOf(c)[0]))
	}
	return ids
}

func logRows(h *fakeHost, status string) []call {
	var out []call
	for _, c := range h.execs("INSERT INTO log") {
		if a := argsOf(c); len(a) > 4 && a[4] == status {
			out = append(out, c)
		}
	}
	return out
}

func TestDeliverPlatformsAreIndependent(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "google", 0, ""), rowJSON(2, "tiktok", 0, ""), rowJSON(3, "linkedin", 0, ""))
	seen := platformHTTP(t, map[string]fetched{
		"datamanager.googleapis.com": {Status: 200, Body: `{}`},
		"business-api.tiktok.com":    {Status: 500, Body: `oops`},
		"api.linkedin.com":           {Status: 201},
	})
	runDeliver(t)
	if len(*seen) != 3 {
		t.Fatalf("calls = %d", len(*seen))
	}
	if got := strings.Join(deletes(h), ","); got != "1,3" {
		t.Fatalf("deleted rows %q: the TikTok failure must not touch Google or LinkedIn", got)
	}
	if n := len(logRows(h, "sent")); n != 2 {
		t.Fatalf("sent rows %d", n)
	}
	retry := h.execs("UPDATE queue SET attempts = attempts + 1")
	if len(retry) != 1 || fmt.Sprint(argsOf(retry[0])[0]) != "2" || argsOf(retry[0])[1] != testNow.Add(time.Minute).Format(time.RFC3339Nano) {
		t.Fatalf("retry %+v", retry)
	}
	if n := len(logRows(h, "retrying")); n != 1 {
		t.Fatalf("retrying rows %d", n)
	}
	// Event ids are the submission id on all three.
	for _, req := range *seen {
		if !strings.Contains(string(req.Body), "7f3c1d52-0000-4000-8000-00000000a91e") {
			t.Fatalf("%s lacks the submission id", req.URL)
		}
	}
}

func TestDeliverHashesOnlyWhenNoClickID(t *testing.T) {
	deliveryHost(t, rowJSON(1, "google", 0, ""))
	seen := platformHTTP(t, map[string]fetched{"datamanager.googleapis.com": {Status: 200}})
	runDeliver(t)
	body := string((*seen)[0].Body)
	if len(*seen) != 1 || strings.Contains(body, "gclid") || !strings.Contains(body, "emailAddress") {
		t.Fatalf("body %s", body)
	}
}

func TestDeliverJoinsPostedClickIDs(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "google", 0, ""))
	h.query["FROM clicks WHERE submission_id"] = `{"gclid":"CLICK-1","evil":"x"}`
	seen := platformHTTP(t, map[string]fetched{"datamanager.googleapis.com": {Status: 200}})
	runDeliver(t)
	if !strings.Contains(string((*seen)[0].Body), `"gclid":"CLICK-1"`) {
		t.Fatalf("body %s", (*seen)[0].Body)
	}
	join := h.execs("UPDATE queue SET gclid")
	if len(join) != 1 || argsOf(join[0])[1] != "CLICK-1" {
		t.Fatalf("join %+v", join)
	}
}

func TestDeliverPermanentFailureLoggedOnceAndNotRetried(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "linkedin", 0, ""))
	platformHTTP(t, map[string]fetched{"api.linkedin.com": {Status: 422, Body: `{"message":"bad rule"}`}})
	runDeliver(t)
	failed := logRows(h, "failed")
	if len(failed) != 1 || argsOf(failed[0])[6] != "422" || argsOf(failed[0])[7] != "bad rule" ||
		len(deletes(h)) != 1 || len(h.execs("UPDATE queue SET attempts")) != 0 {
		t.Fatalf("failed %+v, deletes %v", failed, deletes(h))
	}
}

func TestRetryRowsBackOffWithoutLoggingAgain(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 2, ""))
	platformHTTP(t, map[string]fetched{"business-api.tiktok.com": {Status: 200, Body: `{"code":50000,"message":"busy"}`}})
	if err := onRetry(sdk.CronEvent{Name: "retry"}); err != nil {
		t.Fatal(err)
	}
	up := h.execs("UPDATE queue SET attempts = attempts + 1")
	if len(up) != 1 || argsOf(up[0])[1] != testNow.Add(4*time.Minute).Format(time.RFC3339Nano) || len(logRows(h, "retrying")) != 0 {
		t.Fatalf("update %+v", up)
	}
}

func TestRetryCronExpiresAfter24HoursAndPurges(t *testing.T) {
	h := deliveryHost(t)
	if err := onRetry(sdk.CronEvent{Name: "retry"}); err != nil {
		t.Fatal(err)
	}
	exp := h.execs("WITH d AS (DELETE FROM queue WHERE created_at")
	if len(exp) != 1 || argsOf(exp[0])[0] != testNow.Add(-24*time.Hour).Format(time.RFC3339Nano) ||
		!strings.Contains(sqlOf(exp[0]), "'failed', 'expired'") {
		t.Fatalf("expiry %+v", exp)
	}
	if len(h.execs("DELETE FROM log WHERE")) != 1 || len(h.execs("DELETE FROM clicks WHERE")) != 1 {
		t.Fatal("log and clicks must be purged")
	}
}

func TestDeliverNoIdentifierIsSkipped(t *testing.T) {
	h := deliveryHost(t, `{"id":1,"submission_id":"s","form_slug":"contact","platform":"linkedin","em_hash":null,"page_path":"","attempts":0}`)
	seen := platformHTTP(t, map[string]fetched{})
	runDeliver(t)
	skipped := logRows(h, "skipped")
	if len(*seen) != 0 || len(skipped) != 1 || argsOf(skipped[0])[5] != "no_identifier" || len(deletes(h)) != 1 {
		t.Fatalf("skipped %+v", skipped)
	}
}

func TestDeliverRefusedTokenRefreshesOnceThenBreaksAndMailsOnce(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "google", 0, ""))
	seen := platformHTTP(t, map[string]fetched{
		"datamanager.googleapis.com": {Status: 401, Body: `{"error":{"message":"expired"}}`},
		"oauth2.googleapis.com":      tokenJSON("g-new", "", 3600),
	})
	for i := 0; i < 3; i++ {
		runDeliver(t)
	}
	var hosts []string
	for _, r := range *seen {
		u, _ := url.Parse(r.URL)
		hosts = append(hosts, u.Host+u.Path)
	}
	want := "datamanager.googleapis.com/v1/events:ingest,oauth2.googleapis.com/token,datamanager.googleapis.com/v1/events:ingest"
	if strings.Join(hosts, ",") != want {
		t.Fatalf("calls %v: one refresh, one retry, then the platform is left alone", hosts)
	}
	if !broken(platGoogle) || len(h.callsOf("mail.send")) != 1 || len(deletes(h)) != 0 {
		t.Fatalf("broken %v, mails %d, deletes %v", broken(platGoogle), len(h.callsOf("mail.send")), deletes(h))
	}
	if len(h.execs("UPDATE queue SET next_attempt_at")) != 2 {
		t.Fatal("rows of a stopped platform must be paused on later runs")
	}
}

func TestDeliverTikTokAuthCodeBreaksImmediately(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 0, ""))
	platformHTTP(t, map[string]fetched{"business-api.tiktok.com": {Status: 200, Body: `{"code":40105,"message":"token expired"}`}})
	runDeliver(t)
	runDeliver(t)
	if !broken(platTikTok) || len(h.callsOf("mail.send")) != 1 || len(deletes(h)) != 0 {
		t.Fatalf("broken %v mails %d", broken(platTikTok), len(h.callsOf("mail.send")))
	}
}

func TestDeliverLeavesDisabledPlatformAlone(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 0, ""))
	h.configure(`"tiktok_enabled":false`)
	seen := platformHTTP(t, map[string]fetched{})
	runDeliver(t)
	if len(*seen) != 0 || len(h.execs("UPDATE queue SET next_attempt_at")) != 1 {
		t.Fatal("a disabled platform must not be called")
	}
}

func TestDeliverStopsWhenBudgetIsSpentAndReleasesRest(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 0, ""), rowJSON(2, "tiktok", 0, ""), rowJSON(3, "tiktok", 0, ""))
	clock := testNow
	now = func() time.Time { return clock }
	var timeouts []int
	httpDo = func(req request, timeoutMS int) (fetched, error) {
		timeouts = append(timeouts, timeoutMS)
		clock = clock.Add(5 * time.Second)
		return fetched{Status: 200, Body: `{"code":0}`}, nil
	}
	t.Cleanup(func() { httpDo = realHTTPDo })
	runDeliver(t)
	if len(timeouts) != 2 || timeouts[0] != 8000 || timeouts[1] != 3000 {
		t.Fatalf("timeouts %v", timeouts)
	}
	if rel := h.execs("UPDATE queue SET lease_until = NULL"); len(rel) != 1 || fmt.Sprint(argsOf(rel[0])[0]) != "3" {
		t.Fatalf("release %+v", rel)
	}
}

func TestDeliveryLeaksNothingToLogsMailOrLogRows(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "google", 0, `,"gclid":"CLICK-SECRET"`), rowJSON(2, "tiktok", 0, `,"ttclid":"CLICK-SECRET"`), rowJSON(3, "linkedin", 0, ""))
	platformHTTP(t, map[string]fetched{
		"datamanager.googleapis.com": {Status: 401, Body: `{"error":{"message":"denied"}}`},
		"oauth2.googleapis.com":      {Status: 400, Body: `{"error":"invalid_grant","error_description":"g-refresh g-access"}`},
		"business-api.tiktok.com":    {Status: 200, Body: `{"code":40002,"message":"bad"}`},
		"api.linkedin.com":           {Status: 503},
	})
	runDeliver(t)
	secrets := []string{sampleEmail, strings.ToLower(sampleEmail), "491512345678", sha("jane.doe@example.com"), sha("janedoe@example.com"),
		sha("+491512345678"), "CLICK-SECRET", "g-access", "g-refresh", "l-access", "l-refresh", "tok-secret", "gsecret", "lsecret"}
	scanned := 0
	for _, c := range h.calls {
		if c.fn != "log" && c.fn != "mail.send" && !(c.fn == "db.exec" && strings.Contains(c.args, "INSERT INTO log")) {
			continue
		}
		scanned++
		for _, s := range secrets {
			if strings.Contains(c.args, s) {
				t.Fatalf("%s call leaks %q: %s", c.fn, s, c.args)
			}
		}
	}
	if scanned < 4 {
		t.Fatalf("scanned only %d calls", scanned)
	}
}

func TestDeliverCronsNeverError(t *testing.T) {
	h := newHost(t)
	h.configure()
	h.panicOn = "db.query"
	if err := onDeliver(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	if err := onRetry(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	h.panicOn = "settings.get"
	if err := onDeliver(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
}

func TestTikTokBrokenFlagIsBoundToTheToken(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 0, ""))
	platformHTTP(t, map[string]fetched{"business-api.tiktok.com": {Status: 200, Body: `{"code":40105,"message":"token expired"}`}})
	runDeliver(t)
	if !broken(platTikTok) {
		t.Fatal("a refused token must break TikTok")
	}
	if strings.Contains(fmt.Sprint(h.kv["status:tiktok"]), "tok-secret") {
		t.Fatal("the token itself must not be stored")
	}
	h.configure(`"notify_email":"owner@example.com"`, `"tiktok_access_token":"tok-new"`)
	if broken(platTikTok) {
		t.Fatal("a new access token must lift the broken state")
	}
	// The new token is refused too: it breaks again and is mailed again.
	runDeliver(t)
	if !broken(platTikTok) || len(h.callsOf("mail.send")) != 2 {
		t.Fatalf("broken %v, mails %d", broken(platTikTok), len(h.callsOf("mail.send")))
	}
}

func TestTikTokTestClearsBrokenAndCardShowsIt(t *testing.T) {
	h := deliveryHost(t)
	h.configure(`"tiktok_test_event_code":"TEST123"`)
	s, _ := loadSettings()
	markBroken(s, platTikTok)
	h.query["jsonb_agg(to_jsonb(t))"] = `[]`
	if res, _ := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/log"}); !strings.Contains(res.Body, `"tiktok":{`) ||
		!strings.Contains(res.Body, `"needs_reconnect":true`) {
		t.Fatalf("card must show needs_reconnect: %s", res.Body)
	}
	platformHTTP(t, map[string]fetched{"business-api.tiktok.com": {Status: 200, Body: `{"code":0,"message":"OK"}`}})
	post(t, "/test", `{"platform":"tiktok"}`)
	if broken(platTikTok) || h.kv["status:tiktok"] != nil {
		t.Fatal("a passing Test must clear the broken state")
	}
}

func TestDeliverPausedRowKeepsPostedClickIDs(t *testing.T) {
	h := deliveryHost(t, rowJSON(1, "tiktok", 0, ""))
	h.configure(`"tiktok_enabled":false`)
	h.query["FROM clicks WHERE submission_id"] = `{"ttclid":"CLICK-1"}`
	platformHTTP(t, map[string]fetched{})
	runDeliver(t)
	join := h.execs("UPDATE queue SET gclid")
	if len(join) != 1 || argsOf(join[0])[4] != "CLICK-1" || len(h.execs("UPDATE queue SET next_attempt_at")) != 1 {
		t.Fatalf("a paused row must keep the posted click IDs: %+v", join)
	}
}

func TestDeliverClickOnlySubmissionIsSentWithPostedClickID(t *testing.T) {
	h := deliveryHost(t, `{"id":1,"submission_id":"s","form_slug":"contact","platform":"google","page_path":"","attempts":0}`)
	h.query["FROM clicks WHERE submission_id"] = `{"gclid":"CLICK-1"}`
	seen := platformHTTP(t, map[string]fetched{"datamanager.googleapis.com": {Status: 200, Body: `{}`}})
	runDeliver(t)
	if len(*seen) != 1 || !strings.Contains(string((*seen)[0].Body), `"gclid":"CLICK-1"`) || len(logRows(h, "skipped")) != 0 {
		t.Fatalf("a click-only submission must be sent: %v", *seen)
	}
}
