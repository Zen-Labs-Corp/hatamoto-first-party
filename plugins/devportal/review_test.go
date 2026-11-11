package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func reviewReq(pattern, id, body string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "POST", Path: pattern, Pattern: pattern, PathValues: map[string]string{"id": id}, Body: body,
		User: &sdk.HTTPUser{ID: "u", Role: "owner"}}
}

func subRow(status, accountStatus string) string {
	return `[{"id":5,"account":7,"kind":"plugin","name":"acme-seo","version":"1.3.0","url":"` + releaseURL + `","source":"https://github.com/acme/acme-seo",` +
		`"title":"Acme SEO","about":"Fast","category":"SEO","site_type":"","screenshot":"","sha256":"ab12","size":2097152,"permissions":["db"],"allowed_hosts":[],` +
		`"description":"d","signer_key":"PUBKEY","status":"` + status + `","login":"acme","account_status":"` + accountStatus + `","email":"dev@example.org"}]`
}

// reviewEnv is a fake database for the review routes: rows for the loads,
// rows_affected per statement prefix, and the recorded statements.
type reviewEnv struct {
	*indexHost
	x         *hostX
	sub       string
	owner     string // names rows; after the claim insert they hold the claimer unless claimRace
	claimRace bool
	revoked   int
	listing   string
	keyRows   string
	affected  map[string]int // statement prefix -> rows_affected (default 1)
	failExec  string         // prefix of an exec that errors
}

func newReviewEnv(t *testing.T, key string) *reviewEnv {
	t.Helper()
	e := &reviewEnv{sub: subRow("waiting", "active"), owner: `[]`, listing: `[]`, keyRows: `[{"pubkey":"PUBKEY","key_id":"0123456789abcdef"}]`, affected: map[string]int{}}
	e.indexHost = newIndexHost(t, key)
	inner := e.fakeHost.db
	e.fakeHost.db = func(c hostCall, out any) error {
		switch {
		case c.fn == "db.query" && strings.Contains(c.sql, "'listed'"):
			return cell(out, `{"waiting":[{"id":5}],"listed":[],"revoked":null}`)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM submissions s JOIN accounts a"):
			return cell(out, e.sub)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM listings g JOIN accounts a"):
			return cell(out, e.listing)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM names WHERE"):
			return cell(out, e.owner)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM revocations WHERE signer_key"):
			return cell(out, `{"n":`+itoa(e.revoked)+`}`)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM keys WHERE account"):
			return cell(out, e.keyRows)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM accounts WHERE id"):
			return cell(out, `[{"email":"dev@example.org"}]`)
		case c.fn == "db.query" && strings.Contains(c.sql, "FROM mail_queue"):
			return cell(out, `[]`)
		case c.fn == "db.exec":
			if e.failExec != "" && strings.HasPrefix(c.sql, e.failExec) {
				return errDB
			}
			if strings.HasPrefix(c.sql, "INSERT INTO names") && !e.claimRace {
				e.owner = `[{"owner":7}]`
			}
			for prefix, n := range e.affected {
				if strings.HasPrefix(c.sql, prefix) {
					return affected(out, n)
				}
			}
		}
		return inner(c, out)
	}
	e.x = &hostX{}
	e.x.install(t)
	return e
}

func decode(t *testing.T, r sdk.HTTPResponse) map[string]any {
	t.Helper()
	var m map[string]any
	if err := json.Unmarshal([]byte(r.Body), &m); err != nil {
		t.Fatalf("body %q: %v", r.Body, err)
	}
	return m
}

func (e *reviewEnv) wrote(prefix string) int { return len(e.execs(prefix)) }

func TestApproveListsPublishesAndMailsOnce(t *testing.T) {
	fixedNow(t, t0)
	_, file, _ := listingKey(t)
	e := newReviewEnv(t, file)
	resp := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", ""))
	m := decode(t, resp)
	if resp.Status != 200 || m["ok"] != true || m["published"] != true || m["warning"] != nil {
		t.Fatalf("%+v", resp)
	}
	if e.wrote("UPDATE submissions SET status = 'listed'") != 1 || e.wrote("INSERT INTO names") != 1 || e.wrote("INSERT INTO listings") != 1 || e.wrote("INSERT INTO published") != 1 {
		t.Errorf("statements: %+v", e.calls)
	}
	g := e.execs("INSERT INTO listings")[0]
	if g.args[0] != "plugin" || g.args[1] != "acme-seo" || g.args[2] != int64(7) || g.args[3] != int64(5) || g.args[4] != "1.3.0" {
		t.Errorf("listing args %v", g.args)
	}
	var entry map[string]any
	if err := json.Unmarshal([]byte(g.args[5].(string)), &entry); err != nil {
		t.Fatal(err)
	}
	if entry["tier"] != "developer" || entry["author"] != "acme" || entry["signer_key"] != "PUBKEY" || entry["sha256"] != "ab12" || entry["title"] != "Acme SEO" || entry["category"] != "SEO" {
		t.Errorf("entry %v", entry)
	}
	if q := e.execs("INSERT INTO mail_queue"); len(q) != 1 || q[0].args[0] != "dev@example.org" || !strings.Contains(q[0].args[1].(string), "acme-seo 1.3.0 is listed") {
		t.Errorf("mail %+v", q)
	}
	requireGuard(t, e.fakeHost)
}

func TestApproveTwiceIsRefused(t *testing.T) {
	e := newReviewEnv(t, "")
	e.sub = subRow("listed", "active")
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 {
		t.Fatalf("%+v", r)
	}
	// Two clicks race: the load saw "waiting" for both, the status update lets one win.
	e = newReviewEnv(t, "")
	e.affected["UPDATE submissions SET status = 'listed'"] = 0
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 {
		t.Fatalf("%+v", r)
	}
	if e.wrote("INSERT INTO names") != 0 || e.wrote("INSERT INTO listings") != 0 || e.wrote("INSERT INTO mail_queue") != 0 {
		t.Errorf("the loser changed something: %+v", e.calls)
	}
}

func TestApproveRefusesWhenAnotherAccountOwnsTheName(t *testing.T) {
	e := newReviewEnv(t, "")
	e.owner = `[{"owner":99}]`
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 || !strings.Contains(r.Body, "another developer") {
		t.Fatalf("%+v", r)
	}
	if e.wrote("UPDATE") != 0 || e.wrote("INSERT") != 0 {
		t.Errorf("something changed: %+v", e.calls)
	}
}

func TestApproveLosingTheNameRaceUndoesTheDecision(t *testing.T) {
	e := newReviewEnv(t, "")
	e.claimRace = true // the claim insert does nothing and a rival owns the name
	inner := e.fakeHost.db
	e.fakeHost.db = func(c hostCall, out any) error {
		if strings.HasPrefix(c.sql, "INSERT INTO names") {
			e.owner = `[{"owner":99}]`
		}
		return inner(c, out)
	}
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 {
		t.Fatalf("%+v", r)
	}
	if e.wrote("UPDATE submissions SET status = 'waiting'") != 1 || e.wrote("INSERT INTO listings") != 0 {
		t.Errorf("%+v", e.calls)
	}
}

func TestApproveRefusesSuspendedAccountRevokedKeyAndUnknownSubmission(t *testing.T) {
	e := newReviewEnv(t, "")
	e.sub = subRow("waiting", "suspended")
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 || e.wrote("UPDATE") != 0 {
		t.Errorf("suspended: %+v", r)
	}
	e = newReviewEnv(t, "")
	e.revoked = 1
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 409 || e.wrote("UPDATE") != 0 {
		t.Errorf("revoked key: %+v", r)
	}
	e = newReviewEnv(t, "")
	e.sub = `[]`
	if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", "")); r.Status != 404 {
		t.Errorf("unknown: %+v", r)
	}
	for _, id := range []string{"x", "0", "-1", ""} {
		if r := handleApprove(reviewReq("/admin/submissions/{id}/approve", id, "")); r.Status != 400 {
			t.Errorf("id %q: %+v", id, r)
		}
	}
}

func TestApproveStaysDecidedWhenMailAndPublishFail(t *testing.T) {
	// No listing key: publish fails. Mail is rate limited: nothing is sent.
	e := newReviewEnv(t, "")
	e.x.mailErr = hostErr("rate_limited")
	resp := handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", ""))
	m := decode(t, resp)
	if resp.Status != 200 || m["ok"] != true || m["published"] != false || !strings.Contains(m["warning"].(string), "listing key is not set") {
		t.Fatalf("%+v", resp)
	}
	if e.wrote("INSERT INTO listings") != 1 || e.wrote("UPDATE submissions SET status = 'waiting'") != 0 {
		t.Errorf("the decision was rolled back: %+v", e.calls)
	}
	// A db failure while publishing gives a generic warning, no 5xx.
	_, file, _ := listingKey(t)
	e = newReviewEnv(t, file)
	e.failExec = "INSERT INTO published"
	resp = handleApprove(reviewReq("/admin/submissions/{id}/approve", "5", ""))
	m = decode(t, resp)
	if resp.Status != 200 || m["published"] != false || m["warning"] == nil {
		t.Fatalf("%+v", resp)
	}
}

func TestRejectStoresTheReasonAndMailsIt(t *testing.T) {
	e := newReviewEnv(t, "")
	resp := handleReject(reviewReq("/admin/submissions/{id}/reject", "5", `{"reason":"  Needs a\nscreenshot \u0007 "}`))
	if resp.Status != 200 || decode(t, resp)["status"] != "rejected" {
		t.Fatalf("%+v", resp)
	}
	up := e.execs("UPDATE submissions SET status = 'rejected'")
	if len(up) != 1 || up[0].args[1] != "Needs a screenshot" {
		t.Fatalf("%+v", up)
	}
	if q := e.execs("INSERT INTO mail_queue"); len(q) != 1 || !strings.Contains(q[0].args[2].(string), "Needs a screenshot") {
		t.Errorf("mail %+v", q)
	}
	requireGuard(t, e.fakeHost)
}

func TestRejectNeedsAReasonAndAWaitingSubmission(t *testing.T) {
	e := newReviewEnv(t, "")
	for _, body := range []string{``, `{}`, `{"reason":"  "}`, `{"reason":"\u0007"}`, `nope`} {
		if r := handleReject(reviewReq("/admin/submissions/{id}/reject", "5", body)); r.Status != 400 {
			t.Errorf("%q: %+v", body, r)
		}
	}
	if e.wrote("UPDATE") != 0 {
		t.Error("an invalid reject wrote")
	}
	e.affected["UPDATE submissions SET status = 'rejected'"] = 0
	if r := handleReject(reviewReq("/admin/submissions/{id}/reject", "5", `{"reason":"no"}`)); r.Status != 409 || e.wrote("INSERT INTO mail_queue") != 0 {
		t.Errorf("decided: %+v", r)
	}
	// A long reason is cut to 200 runes.
	e = newReviewEnv(t, "")
	handleReject(reviewReq("/admin/submissions/{id}/reject", "5", `{"reason":"`+strings.Repeat("é", 300)+`"}`))
	if got := e.execs("UPDATE submissions SET status = 'rejected'")[0].args[1].(string); len([]rune(got)) != 200 {
		t.Errorf("reason %d runes", len([]rune(got)))
	}
}

func TestRevokeVersionRecordsNameAndVersionOnly(t *testing.T) {
	_, file, _ := listingKey(t)
	e := newReviewEnv(t, file)
	e.listing = `[{"id":3,"account":7,"name":"acme-seo","version":"1.3.0","submission":5,"status":"active","email":"dev@example.org"}]`
	resp := handleRevokeVersion(reviewReq("/admin/listings/{id}/revoke", "3", `{"reason":"malware"}`))
	if m := decode(t, resp); resp.Status != 200 || m["published"] != true {
		t.Fatalf("%+v", resp)
	}
	if e.wrote("UPDATE listings SET status = 'revoked'") != 1 || e.wrote("UPDATE submissions SET status = 'revoked'") != 1 {
		t.Errorf("%+v", e.calls)
	}
	rv := e.execs("INSERT INTO revocations")
	if len(rv) != 1 || strings.Contains(rv[0].sql, "signer_key") || rv[0].args[0] != "acme-seo" || rv[0].args[1] != "1.3.0" || rv[0].args[2] != "malware" {
		t.Errorf("revocation %+v", rv)
	}
	if q := e.execs("INSERT INTO mail_queue"); len(q) != 1 {
		t.Errorf("mail %+v", q)
	}
	requireGuard(t, e.fakeHost)
}

func TestRevokeVersionRefusals(t *testing.T) {
	e := newReviewEnv(t, "")
	if r := handleRevokeVersion(reviewReq("/admin/listings/{id}/revoke", "3", `{}`)); r.Status != 400 {
		t.Errorf("no reason: %+v", r)
	}
	if r := handleRevokeVersion(reviewReq("/admin/listings/{id}/revoke", "3", `{"reason":"x"}`)); r.Status != 404 {
		t.Errorf("unknown: %+v", r)
	}
	e.listing = `[{"id":3,"account":7,"name":"a","version":"1","submission":5,"status":"revoked","email":""}]`
	e.affected["UPDATE listings SET status = 'revoked'"] = 0
	if r := handleRevokeVersion(reviewReq("/admin/listings/{id}/revoke", "3", `{"reason":"x"}`)); r.Status != 409 || e.wrote("INSERT INTO revocations") != 0 {
		t.Errorf("twice: %+v", r)
	}
}

func TestRevokeKeySuspendsListingsAndRecordsTheKey(t *testing.T) {
	_, file, _ := listingKey(t)
	e := newReviewEnv(t, file)
	e.affected["UPDATE listings SET status = 'suspended'"] = 2
	resp := handleRevokeKey(reviewReq("/admin/accounts/{id}/revoke-key", "7", `{"reason":"key leaked"}`))
	m := decode(t, resp)
	if resp.Status != 200 || m["suspended"] != float64(2) || m["published"] != true {
		t.Fatalf("%+v", resp)
	}
	rv := e.execs("INSERT INTO revocations")
	if len(rv) != 1 || rv[0].args[0] != "PUBKEY" || rv[0].args[1] != "key leaked" {
		t.Errorf("revocation %+v", rv)
	}
	if up := e.execs("UPDATE listings SET status = 'suspended'"); len(up) != 1 || up[0].args[0] != int64(7) {
		t.Errorf("suspend %+v", up)
	}
	if len(e.execs("INSERT INTO mail_queue")) != 1 {
		t.Error("developer not mailed")
	}
	requireGuard(t, e.fakeHost)
}

func TestRevokeKeyIsSafeToRepeatAndNeedsAKey(t *testing.T) {
	e := newReviewEnv(t, "")
	e.affected["INSERT INTO revocations"] = 0 // already revoked
	resp := handleRevokeKey(reviewReq("/admin/accounts/{id}/revoke-key", "7", `{"reason":"again"}`))
	if resp.Status != 200 || e.wrote("UPDATE listings SET status = 'suspended'") != 1 || e.wrote("INSERT INTO mail_queue") != 0 {
		t.Errorf("repeat: %+v %+v", resp, e.calls)
	}
	e = newReviewEnv(t, "")
	e.keyRows = `[]`
	if r := handleRevokeKey(reviewReq("/admin/accounts/{id}/revoke-key", "7", `{"reason":"x"}`)); r.Status != 404 || e.wrote("INSERT") != 0 {
		t.Errorf("no key: %+v", r)
	}
	e = newReviewEnv(t, "")
	if r := handleRevokeKey(reviewReq("/admin/accounts/{id}/revoke-key", "7", ``)); r.Status != http.StatusBadRequest {
		t.Errorf("no reason: %+v", r)
	}
}

func TestReviewListAlwaysReturnsThreeLists(t *testing.T) {
	e := newReviewEnv(t, "")
	resp := handleReviewList(sdk.HTTPRequest{Method: "GET", Path: "/admin/submissions", Pattern: "/admin/submissions", User: &sdk.HTTPUser{Role: "owner"}})
	var got map[string][]json.RawMessage
	if err := json.Unmarshal([]byte(resp.Body), &got); err != nil || resp.Status != 200 {
		t.Fatalf("%+v %v", resp, err)
	}
	if len(got["waiting"]) != 1 || got["listed"] == nil || got["revoked"] == nil {
		t.Errorf("%s", resp.Body)
	}
	requireGuard(t, e.fakeHost)
	e.fakeHost.db = func(hostCall, any) error { return errDB }
	if r := handleReviewList(sdk.HTTPRequest{}); r.Status != 503 || strings.Contains(r.Body, "db down") {
		t.Errorf("%+v", r)
	}
}
