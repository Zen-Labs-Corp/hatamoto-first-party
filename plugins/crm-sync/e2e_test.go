package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const (
	e2eEmail = "ann@example.com"

	e2eHubSpot    = `{"provider":"hubspot","access_token":"tok","forms":["contact"]}`
	e2ePipedrive  = `{"provider":"pipedrive","api_token":"tok","company_domain":"acme","forms":["contact"]}`
	e2eSalesforce = `{"provider":"salesforce","my_domain":"acme.my.salesforce.com","client_id":"cid","client_secret":"sec","forms":["contact"]}`
)

func e2eEvent(email string, fields map[string]any) sdk.FormSubmittedEvent {
	fields["email"] = email
	return submission(fields)
}

// queuedJob rebuilds the job from the arguments of the queue insert the hook
// made, so a test delivers exactly what was stored.
func queuedJob(t *testing.T, h *fakeHost) job {
	t.Helper()
	ins := h.execs("INSERT INTO queue")
	if len(ins) != 1 {
		t.Fatalf("queue inserts = %d", len(ins))
	}
	a := ins[0].args
	j := job{SubmissionID: a[0].(string), FormSlug: a[1].(string), Provider: a[2].(string), Email: a[3].(string), SourcePath: a[5].(string)}
	if err := json.Unmarshal([]byte(a[4].(string)), &j.Fields); err != nil {
		t.Fatal(err)
	}
	j.SubmittedAt, _ = time.Parse(time.RFC3339Nano, a[6].(string))
	j.NextAttemptAt = j.SubmittedAt
	return j
}

func claimRows(t *testing.T, js ...job) string {
	t.Helper()
	b, err := json.Marshal(js)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

// useRealProviders delivers through the registered provider with fake.
func useRealProviders(t *testing.T, fake *fakeFetch) {
	t.Helper()
	old := deliverFn
	deliverFn = func(c cfg, j job, m []mapRow) result { return providers[c.Provider].upsert(j, c, m, fake.fetch) }
	t.Cleanup(func() { deliverFn = old })
}

func hasPrefixURL(r fetchReq, method, suffix string) bool {
	return r.Method == method && strings.Contains(r.URL, suffix)
}

// hubspotHandler answers the upsert as created.
func hubspotHandler(t *testing.T) func(fetchReq) fetchResp {
	return func(r fetchReq) fetchResp {
		switch {
		case hasPrefixURL(r, "POST", "/batch/upsert"):
			return fetchResp{Status: 200, Body: `{"results":[{"id":"501","new":true}]}`}
		case hasPrefixURL(r, "POST", "/objects/notes"):
			return fetchResp{Status: 201, Body: `{"id":"9"}`}
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return fetchResp{}
	}
}

// pipedriveHandler finds the person, so the delivery is an update.
func pipedriveHandler(t *testing.T) func(fetchReq) fetchResp {
	return func(r fetchReq) fetchResp {
		switch {
		case hasPrefixURL(r, "GET", "/api/v2/persons/search"):
			return pdResp(pdPerson)
		case hasPrefixURL(r, "GET", "/api/v2/organizations/search"):
			return pdResp(pdNoHit)
		case hasPrefixURL(r, "POST", "/api/v2/organizations"):
			return fetchResp{Status: 201, Body: `{"data":{"id":7}}`}
		case hasPrefixURL(r, "PATCH", "/api/v2/persons/42"):
			return pdResp(pdSaved)
		case hasPrefixURL(r, "GET", "/api/v1/leads"):
			return pdResp(pdLeadGT)
		case hasPrefixURL(r, "POST", "/api/v1/notes"):
			return fetchResp{Status: 201, Body: `{"data":{"id":1}}`}
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return fetchResp{}
	}
}

// salesforceHandler finds no lead, so the delivery is a create.
func salesforceHandler(t *testing.T, accessToken string) func(fetchReq) fetchResp {
	return func(r fetchReq) fetchResp {
		switch {
		case hasPrefixURL(r, "POST", "/services/oauth2/token"):
			return fetchResp{Status: 200, Body: `{"access_token":"` + accessToken + `","instance_url":"https://acme.my.salesforce.com"}`}
		case hasPrefixURL(r, "GET", "/query"):
			return sfResp(sfNoRec)
		case hasPrefixURL(r, "POST", "/sobjects/Lead"):
			return fetchResp{Status: 201, Body: sfCreate}
		case hasPrefixURL(r, "POST", "/sobjects/Task"):
			return fetchResp{Status: 201, Body: `{"id":"00T000000000001AAA","success":true}`}
		}
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return fetchResp{}
	}
}

// endToEnd runs the hook, a claim of the stored row and runQueue, and checks
// the one log row and the deleted queue row.
func endToEnd(t *testing.T, settings string, handler func(*testing.T) func(fetchReq) fetchResp, wantStatus, wantID, wantURL string) {
	t.Helper()
	h := &fakeHost{settings: settings}
	h.install(t)
	if err := kvSet("hs_portal", hsPortal{PortalID: 1234, UIDomain: "app.hubspot.com"}, 0); err != nil {
		t.Fatal(err)
	}
	if err := onFormSubmitted(e2eEvent(e2eEmail, map[string]any{"first_name": "Ann", "message": "hello"})); err != nil {
		t.Fatal(err)
	}
	j := queuedJob(t, h)
	h.queryRows = []string{claimRows(t, j)}
	fake := &fakeFetch{t: t, handler: handler(t)}
	useRealProviders(t, fake)
	runQueue(true)
	if got := len(h.execs("DELETE FROM queue")); got != 1 {
		t.Fatalf("queue deletes = %d", got)
	}
	logs := h.execs("INSERT INTO log")
	if len(logs) != 1 {
		t.Fatalf("log rows = %d", len(logs))
	}
	a := logs[0].args
	if a[4] != wantStatus || a[5] != wantID || a[6] != wantURL || a[8] != "" {
		t.Fatalf("log row %v", a)
	}
	if len(fake.reqs) < 2 {
		t.Fatalf("requests = %d", len(fake.reqs))
	}
}

func TestEndToEndHubSpotCreated(t *testing.T) {
	endToEnd(t, e2eHubSpot, hubspotHandler, "created", "501", "https://app.hubspot.com/contacts/1234/record/0-1/501")
}

func TestEndToEndPipedriveUpdated(t *testing.T) {
	endToEnd(t, e2ePipedrive, pipedriveHandler, "updated", "42", "https://acme.pipedrive.com/leads/inbox/0f1e2d3c")
}

func TestEndToEndSalesforceCreated(t *testing.T) {
	endToEnd(t, e2eSalesforce, func(t *testing.T) func(fetchReq) fetchResp { return salesforceHandler(t, "AT") },
		"created", "00Q000000000009AAA", "https://acme.my.salesforce.com/00Q000000000009AAA")
}

func TestRetryPathThenSuccess(t *testing.T) {
	h := &fakeHost{settings: e2eHubSpot}
	h.install(t)
	if err := onFormSubmitted(e2eEvent(e2eEmail, map[string]any{"first_name": "Ann"})); err != nil {
		t.Fatal(err)
	}
	j := queuedJob(t, h)
	h.queryRows = []string{claimRows(t, j)}
	useRealProviders(t, newFake(t, fetchResp{Status: 503}))
	runQueue(true)
	up := h.execs("UPDATE queue SET attempts")
	if len(up) != 1 || len(h.execs("DELETE FROM queue")) != 0 {
		t.Fatalf("first run: updates %d, deletes %d", len(up), len(h.execs("DELETE FROM queue")))
	}
	if got, want := up[0].args[1], ts(now().Add(5*time.Minute)); got != want {
		t.Fatalf("next attempt %v, want %v", got, want)
	}
	if rows := h.logRows(); len(rows) != 1 || rows[0][0] != "retrying" {
		t.Fatalf("log %v", rows)
	}

	j.Attempts = 1
	h.queryRows = []string{claimRows(t, j)}
	fake := &fakeFetch{t: t, handler: hubspotHandler(t)}
	useRealProviders(t, fake)
	runQueue(false)
	if len(h.execs("DELETE FROM queue")) != 1 {
		t.Fatalf("second run did not delete the row")
	}
	rows := h.logRows()
	if len(rows) != 2 || rows[1][0] != "created" {
		t.Fatalf("log %v", rows)
	}
}

func TestExpiredRowNeverDelivered(t *testing.T) {
	h := &fakeHost{settings: e2eHubSpot, queryRows: []string{`[]`}}
	h.install(t)
	fake := &fakeFetch{t: t, handler: func(r fetchReq) fetchResp {
		t.Fatalf("unexpected request %s %s", r.Method, r.URL)
		return fetchResp{}
	}}
	useRealProviders(t, fake)
	if err := onRetry(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	calls := h.execs("WITH d AS (DELETE FROM queue")
	if len(calls) != 1 || calls[0].args[0] != ts(now().Add(-24*time.Hour)) {
		t.Fatalf("expire calls %+v", calls)
	}
	// The expiry runs before the claim, and the claim itself refuses old rows.
	var order []string
	for _, c := range h.calls {
		switch {
		case strings.HasPrefix(c.sql, "WITH d AS"):
			order = append(order, "expire")
		case strings.HasPrefix(c.sql, "WITH c AS"):
			order = append(order, "claim")
			if !strings.Contains(c.sql, "created_at >= $2") || c.args[1] != ts(now().Add(-24*time.Hour)) {
				t.Errorf("claim does not filter old rows: %s %v", c.sql, c.args)
			}
		}
	}
	if len(order) != 2 || order[0] != "expire" || order[1] != "claim" {
		t.Fatalf("order %v", order)
	}
	if len(fake.reqs) != 0 {
		t.Fatalf("fetches = %d", len(fake.reqs))
	}
}
