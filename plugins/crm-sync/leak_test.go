package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

var sentinels = []string{"SENTINEL-TOKEN", "SENTINEL-SECRET", "SENTINEL-ACCESS", "SENTINEL-VALUE", "sentinel.person@example.com"}

const (
	leakEmail = "sentinel.person@example.com"
	leakBody  = `SENTINEL-TOKEN SENTINEL-SECRET SENTINEL-ACCESS SENTINEL-VALUE sentinel.person@example.com`
)

var leakSettings = map[string]string{
	"hubspot":    `{"provider":"hubspot","access_token":"SENTINEL-TOKEN","forms":["contact"]}`,
	"pipedrive":  `{"provider":"pipedrive","api_token":"SENTINEL-TOKEN","company_domain":"acme","forms":["contact"]}`,
	"salesforce": `{"provider":"salesforce","my_domain":"acme.my.salesforce.com","client_id":"cid","client_secret":"SENTINEL-SECRET","forms":["contact"]}`,
}

// leakAnswer is the failing answer for one scenario; every body carries all
// sentinels, as a provider that echoes the request would.
type leakAnswer struct {
	name string
	resp fetchResp
}

var leakAnswers = []leakAnswer{
	{"400", fetchResp{Status: 400, Body: `{"message":"` + leakBody + ` [{\"isValid\":false,\"error\":\"PROPERTY_DOESNT_EXIST\",\"name\":\"SENTINEL-VALUE\"}]","errors":[{"errorCode":"INVALID_FIELD","message":"No such column 'SENTINEL-VALUE'","fields":["SENTINEL-VALUE"]}],"error":"` + leakBody + `"}`}},
	{"401", fetchResp{Status: 401, Body: `[{"errorCode":"INVALID_SESSION_ID","message":"` + leakBody + `"}]`}},
	{"403", fetchResp{Status: 403, Body: leakBody}},
	{"500", fetchResp{Status: 500, Body: leakBody}},
	{"503", fetchResp{Status: 503, Body: leakBody}},
	{"200 junk", fetchResp{Status: 200, Body: leakBody}},
	{"timeout", fetchResp{Code: sdk.ErrTimeout}},
	{"blocked", fetchResp{Code: sdk.ErrBlocked}},
}

// leakSurface collects every string the plugin wrote to the host log and the
// delivery log table, and the responses it gave.
type leakSurface struct {
	t      *testing.T
	h      *fakeHost
	routes []string
}

func (s *leakSurface) scan(where string) {
	s.t.Helper()
	var all []string
	all = append(all, s.h.hostLogs()...)
	for _, c := range s.h.execs("INSERT INTO log") {
		all = append(all, fmt.Sprint(c.args...))
	}
	for _, c := range s.h.execs("WITH d AS") {
		all = append(all, fmt.Sprint(c.args...))
	}
	all = append(all, s.routes...)
	for _, str := range all {
		for _, sen := range sentinels {
			if strings.Contains(str, sen) {
				s.t.Errorf("%s: %q leaks %q", where, str, sen)
			}
		}
	}
}

func (s *leakSurface) route(method, pattern, body string) {
	s.t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern, Body: body})
	if err != nil {
		s.t.Fatalf("%s %s returned an error: %v", method, pattern, err)
	}
	s.routes = append(s.routes, res.Body)
}

// withFetchAnswers makes the host's http.fetch answer through handler, so the
// routes run the real provider code.
func withFetchAnswers(handler func(fetchReq) fetchResp) {
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn != "http.fetch" {
			return inner(fn, a, out)
		}
		m := a.(map[string]any)
		resp := handler(fetchReq{Method: m["method"].(string), URL: m["url"].(string)})
		if resp.Code != "" {
			return &sdk.HostError{Code: resp.Code, Message: leakBody}
		}
		b, _ := json.Marshal(map[string]any{"status": resp.Status, "body": resp.Body})
		return json.Unmarshal(b, out)
	}
}

func TestNoSecretsOrPersonalDataLeak(t *testing.T) {
	for name, settings := range leakSettings {
		for _, tokenOK := range []bool{true, false} {
			for _, ans := range leakAnswers {
				t.Run(fmt.Sprintf("%s/token-ok=%v/%s", name, tokenOK, ans.name), func(t *testing.T) {
					h := &fakeHost{settings: settings, forms: `{"items":[{"slug":"contact","fields":[{"name":"email","label":"Email"}]}]}`}
					h.install(t)
					s := &leakSurface{t: t, h: h}
					handler := func(r fetchReq) fetchResp {
						if tokenOK && strings.Contains(r.URL, "/oauth2/token") {
							return fetchResp{Status: 200, Body: `{"access_token":"SENTINEL-ACCESS","instance_url":"https://acme.my.salesforce.com"}`}
						}
						return ans.resp
					}
					fake := &fakeFetch{t: t, handler: handler}

					// Hook: queued, then skipped paths.
					if err := onFormSubmitted(e2eEvent(leakEmail, map[string]any{"first_name": "SENTINEL-VALUE", "message": "SENTINEL-VALUE"})); err != nil {
						t.Fatal(err)
					}
					j := queuedJob(t, h)
					queue := h.execs("INSERT INTO queue")[0]
					queueText := fmt.Sprint(queue.args...)
					if !strings.Contains(queueText, leakEmail) || !strings.Contains(queueText, "SENTINEL-VALUE") {
						t.Fatalf("queue row lacks the email or values: %s", queueText)
					}
					for _, sec := range []string{"SENTINEL-TOKEN", "SENTINEL-SECRET", "SENTINEL-ACCESS"} {
						if strings.Contains(queueText, sec) {
							t.Fatalf("queue row holds %s", sec)
						}
					}
					bad := submission(map[string]any{"email": "SENTINEL-VALUE sentinel.person@example.com not an email"})
					bad.SubmissionID = "sub-bad"
					onFormSubmitted(bad)

					// Queue path: claim, deliver, settle, for a first and a later attempt.
					for _, attempts := range []int{0, 3} {
						j.Attempts = attempts
						h.queryRows = []string{claimRows(t, j)}
						useRealProviders(t, fake)
						runQueue(attempts == 0)
					}
					if len(h.execs("INSERT INTO log")) < 2 {
						t.Fatalf("expected log rows, got %d", len(h.execs("INSERT INTO log")))
					}
					s.scan("queue path")

					// Routes, with the same failing answers.
					withFetchAnswers(handler)
					var rows []string
					for _, c := range h.execs("INSERT INTO log") {
						a := c.args
						b, _ := json.Marshal(map[string]any{"created_at": a[0], "submission_id": a[1], "form_slug": a[2], "provider": a[3], "status": a[4], "record_id": a[5], "record_url": a[6], "code": a[7], "message": a[8]})
						rows = append(rows, string(b))
					}
					h.queryRows = []string{`[` + strings.Join(rows, ",") + `]`}
					s.route("GET", "/log", "")
					s.route("POST", "/test", "")
					s.route("GET", "/fields", "")
					s.route("GET", "/map", "")
					s.route("PUT", "/map", `{"rows":[{"form_field":"a","crm_field":"x"},{"form_field":"b","crm_field":"x"}]}`)
					s.route("PUT", "/map", `{"rows":[{"form_field":"a","crm_field":"x"}]}`)
					s.route("PUT", "/map", `SENTINEL-VALUE`)
					s.scan("routes")
				})
			}
		}
	}
}

func TestNoSecretsOnSuccess(t *testing.T) {
	handlers := map[string]func(*testing.T) func(fetchReq) fetchResp{
		"hubspot":    hubspotHandler,
		"pipedrive":  pipedriveHandler,
		"salesforce": func(t *testing.T) func(fetchReq) fetchResp { return salesforceHandler(t, "SENTINEL-ACCESS") },
	}
	for name, settings := range leakSettings {
		t.Run(name, func(t *testing.T) {
			h := &fakeHost{settings: settings}
			h.install(t)
			s := &leakSurface{t: t, h: h}
			if err := onFormSubmitted(e2eEvent(leakEmail, map[string]any{"first_name": "SENTINEL-VALUE", "message": "SENTINEL-VALUE"})); err != nil {
				t.Fatal(err)
			}
			h.queryRows = []string{claimRows(t, queuedJob(t, h))}
			useRealProviders(t, &fakeFetch{t: t, handler: handlers[name](t)})
			runQueue(true)
			if rows := h.logRows(); len(rows) != 1 || rows[0][0] == "failed" {
				t.Fatalf("log %v", rows)
			}
			s.scan("success path")
		})
	}
}
