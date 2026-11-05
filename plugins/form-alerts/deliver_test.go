package main

import (
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const (
	sampleSlack = "https://hooks.slack.com/services/T0/B0/SLACKSECRET"
	sampleTeams = "https://prod-1.westus.logic.azure.com:443/workflows/w/triggers/manual/paths/invoke?sig=TEAMSSIG"
	sampleGChat = "https://chat.googleapis.com/v1/spaces/S/messages?key=GKEY&token=GTOKEN"
	sampleToken = "123456:TELEGRAMSECRETTOKEN0000"
	allSettings = `{"slack_enabled":true,"slack_webhook_url":"` + sampleSlack + `",` +
		`"teams_enabled":true,"teams_workflow_url":"` + sampleTeams + `",` +
		`"gchat_enabled":true,"gchat_webhook_url":"` + sampleGChat + `",` +
		`"telegram_enabled":true,"telegram_bot_token":"` + sampleToken + `","telegram_chat_id":"-1001234"}`
)

var secrets = []string{"SLACKSECRET", "TEAMSSIG", "GKEY", "GTOKEN", "TELEGRAMSECRETTOKEN0000"}

// fakeFetch answers requests from a script, one response per call, and records them.
type fakeFetch struct {
	t    *testing.T
	resp []fetchResp
	reqs []fetchReq
}

func newFake(t *testing.T, resp ...fetchResp) *fakeFetch {
	f := &fakeFetch{t: t, resp: resp}
	fetchFn = func(r fetchReq) fetchResp {
		f.reqs = append(f.reqs, r)
		if len(f.reqs) > len(f.resp) {
			f.t.Fatalf("unexpected extra request to %s", r.URL)
		}
		return f.resp[len(f.reqs)-1]
	}
	return f
}

func queued(channels ...string) string {
	var rows []string
	for i, ch := range channels {
		rows = append(rows, `{"id":`+string(rune('1'+i))+`,"submission_id":"42","form_slug":"contact","channel":"`+ch+`","body":"{}","attempts":0,"created_at":"2026-11-04T09:59:00+00:00"}`)
	}
	return "[" + strings.Join(rows, ",") + "]"
}

// writes returns the queue statements and log statuses recorded.
func (h *fakeHost) queueWrites() (updates, deletes []hostCallRec, logs []string) {
	for _, c := range h.calls {
		if c.fn != "db.exec" {
			continue
		}
		switch {
		case strings.HasPrefix(c.sql, "UPDATE queue SET attempts"):
			updates = append(updates, c)
		case strings.HasPrefix(c.sql, "DELETE FROM queue"):
			deletes = append(deletes, c)
		case strings.HasPrefix(c.sql, "INSERT INTO log"):
			logs = append(logs, c.args[4].(string))
		}
	}
	return
}

func runDrain(t *testing.T, channels []string, resp ...fetchResp) (*fakeHost, *fakeFetch) {
	t.Helper()
	h := &fakeHost{settings: allSettings, query: queued(channels...)}
	h.install(t)
	f := newFake(t, resp...)
	drain(now())
	return h, f
}

func TestSentIsDeletedAndLogged(t *testing.T) {
	h, f := runDrain(t, []string{chanSlack}, fetchResp{Status: 200})
	_, del, logs := h.queueWrites()
	if len(del) != 1 || len(logs) != 1 || logs[0] != "sent" {
		t.Fatalf("deletes %d logs %v", len(del), logs)
	}
	if r := f.reqs[0]; r.URL != sampleSlack || r.Method != "POST" || r.Headers["Content-Type"] != "application/json" {
		t.Fatalf("request %+v", r)
	}
}

func TestRejectedIsFailedNotRetried(t *testing.T) {
	h, _ := runDrain(t, []string{chanSlack}, fetchResp{Status: 400})
	up, del, logs := h.queueWrites()
	if len(up) != 0 || len(del) != 1 || logs[0] != "failed" {
		t.Fatalf("updates %d deletes %d logs %v", len(up), len(del), logs)
	}
}

func TestRetryAfterIsHonoured(t *testing.T) {
	h, _ := runDrain(t, []string{chanSlack}, fetchResp{Status: 429, RetryAfter: 10 * time.Minute})
	up, del, logs := h.queueWrites()
	if len(del) != 0 || len(up) != 1 || logs[0] != "retrying" || up[0].args[2] != "2026-11-04T10:10:00Z" {
		t.Fatalf("updates %+v deletes %d logs %v", up, len(del), logs)
	}
}

func TestServerErrorBacksOff(t *testing.T) {
	h, _ := runDrain(t, []string{chanGChat}, fetchResp{Status: 503})
	up, _, _ := h.queueWrites()
	if len(up) != 1 || up[0].args[2] != "2026-11-04T10:05:00Z" {
		t.Fatalf("updates %+v", up)
	}
}

func TestHostTimeoutRetriesAndBlockedFails(t *testing.T) {
	h, _ := runDrain(t, []string{chanSlack}, fetchResp{Code: sdk.ErrTimeout})
	if up, _, _ := h.queueWrites(); len(up) != 1 {
		t.Fatal("timeout must be retried")
	}
	h, _ = runDrain(t, []string{chanSlack}, fetchResp{Code: sdk.ErrBlocked})
	if up, del, _ := h.queueWrites(); len(up) != 0 || len(del) != 1 {
		t.Fatal("blocked must fail")
	}
}

func TestOneFailingChannelDoesNotStopTheOthers(t *testing.T) {
	h, f := runDrain(t, []string{chanSlack, chanTeams, chanTelegram},
		fetchResp{Status: 503}, fetchResp{Status: 202}, fetchResp{Status: 400})
	up, del, logs := h.queueWrites()
	if len(f.reqs) != 3 || len(up) != 1 || len(del) != 2 || strings.Join(logs, ",") != "retrying,sent,failed" {
		t.Fatalf("requests %d updates %d deletes %d logs %v", len(f.reqs), len(up), len(del), logs)
	}
	if f.reqs[2].URL != "https://api.telegram.org/bot"+sampleToken+"/sendMessage" {
		t.Fatalf("telegram URL %q", f.reqs[2].URL)
	}
}

func TestBudgetCutLeavesRowsWithoutAttempt(t *testing.T) {
	h := &fakeHost{settings: allSettings, query: queued(chanSlack, chanTeams, chanGChat)}
	h.install(t)
	clock := now()
	now = func() time.Time { return clock }
	f := newFake(t, fetchResp{Status: 200})
	fetchFn = func(r fetchReq) fetchResp {
		f.reqs = append(f.reqs, r)
		clock = clock.Add(7 * time.Second) // the first send used most of the budget
		return fetchResp{Status: 200}
	}
	drain(clock)
	up, del, logs := h.queueWrites()
	if len(f.reqs) != 1 || len(up) != 0 || len(del) != 1 || len(logs) != 1 {
		t.Fatalf("requests %d updates %d deletes %d logs %v", len(f.reqs), len(up), len(del), logs)
	}
	released := 0
	for _, c := range h.calls {
		if strings.HasPrefix(c.sql, "UPDATE queue SET lease_until") {
			released++
		}
	}
	if released != 2 {
		t.Fatalf("released %d rows, want the 2 unsent ones", released)
	}
}

func TestSlowCutBecomesRetry(t *testing.T) {
	if r := slowCut(result{kind: kindBudget}, cronBudget, cronBudget); r.kind != kindRetry || r.reason != "too slow" {
		t.Fatalf("%+v", r)
	}
	if r := slowCut(result{kind: kindBudget}, 2*time.Second, cronBudget); r.kind != kindBudget {
		t.Fatalf("%+v", r)
	}
}

func TestChannelNoLongerSetUpFailsOnce(t *testing.T) {
	h := &fakeHost{settings: `{"slack_enabled":false,"slack_webhook_url":"` + sampleSlack + `"}`, query: queued(chanSlack, chanTeams)}
	h.install(t)
	newFake(t)
	drain(now())
	up, del, logs := h.queueWrites()
	if len(up) != 0 || len(del) != 2 || strings.Join(logs, ",") != "failed,failed" {
		t.Fatalf("updates %d deletes %d logs %v", len(up), len(del), logs)
	}
}

func TestClassifyTable(t *testing.T) {
	for _, tc := range []struct {
		name string
		resp fetchResp
		kind string
	}{
		{"200", fetchResp{Status: 200}, kindSent},
		{"202", fetchResp{Status: 202}, kindSent},
		{"400", fetchResp{Status: 400}, kindFail},
		{"401", fetchResp{Status: 401}, kindFail},
		{"404", fetchResp{Status: 404}, kindFail},
		{"410", fetchResp{Status: 410}, kindFail},
		{"422", fetchResp{Status: 422}, kindFail},
		{"408", fetchResp{Status: 408}, kindRetry},
		{"429", fetchResp{Status: 429}, kindRetry},
		{"500", fetchResp{Status: 500}, kindRetry},
		{"503", fetchResp{Status: 503}, kindRetry},
		{"timeout", fetchResp{Code: sdk.ErrTimeout}, kindRetry},
		{"unavailable", fetchResp{Code: sdk.ErrUnavailable}, kindRetry},
		{"rate_limited", fetchResp{Code: sdk.ErrRateLimited}, kindRetry},
		{"internal", fetchResp{Code: sdk.ErrInternal}, kindRetry},
		{"capability_denied", fetchResp{Code: sdk.ErrCapabilityDenied}, kindFail},
		{"blocked", fetchResp{Code: sdk.ErrBlocked}, kindFail},
		{"invalid_argument", fetchResp{Code: sdk.ErrInvalidArgument}, kindFail},
		{"budget", fetchResp{Code: budgetCode}, kindBudget},
	} {
		if r := classify(tc.resp); r.kind != tc.kind {
			t.Errorf("%s: kind %q, want %q", tc.name, r.kind, tc.kind)
		}
	}
}

func TestNoSecretReachesTheDatabaseOrTheLog(t *testing.T) {
	h, _ := runDrain(t, []string{chanSlack, chanTeams, chanGChat, chanTelegram},
		fetchResp{Status: 503}, fetchResp{Status: 400}, fetchResp{Code: sdk.ErrBlocked}, fetchResp{Status: 200})
	if len(h.calls) == 0 {
		t.Fatal("nothing recorded")
	}
	for _, c := range h.calls {
		all := c.sql + c.msg + strings.Join(argStrings(c.args), " ")
		for _, s := range secrets {
			if strings.Contains(all, s) {
				t.Fatalf("%s call leaks %q: %+v", c.fn, s, c)
			}
		}
	}
}

func argStrings(args []any) []string {
	var out []string
	for _, a := range args {
		if s, ok := a.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func TestDoFetchReadsRetryAfter(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	for _, tc := range []struct {
		headers string
		body    string
		status  int
		want    time.Duration
	}{
		{`{"retry-after":"120"}`, "", 429, 2 * time.Minute},
		{`{"retry-after":"Wed, 04 Nov 2026 10:05:00 GMT"}`, "", 429, 5 * time.Minute},
		{`{}`, `{"ok":false,"error_code":429,"parameters":{"retry_after":17}}`, 429, 17 * time.Second},
		{`{"retry-after":"junk"}`, "", 503, 0},
	} {
		callHost = func(fn string, a, out any) error {
			return unmarshal(`{"status":`+itoa(tc.status)+`,"headers":`+tc.headers+`,"body":`+quote(tc.body)+`}`, out)
		}
		if r := doFetch(fetchReq{}); r.RetryAfter != tc.want || r.Status != tc.status {
			t.Errorf("%s %s: %+v", tc.headers, tc.body, r)
		}
	}
}

func TestDoFetchKeepsOnlyTheHostCode(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	callHost = func(string, any, any) error {
		return &sdk.HostError{Code: sdk.ErrTimeout, Message: sampleSlack + " timed out"}
	}
	if r := doFetch(fetchReq{Method: "POST", URL: sampleSlack}); r.Code != sdk.ErrTimeout || r.Status != 0 || r.Body != "" {
		t.Fatalf("%+v", r)
	}
	callHost = func(string, any, any) error { return errors.New("boom " + sampleSlack) }
	if r := doFetch(fetchReq{}); r.Code != sdk.ErrInternal {
		t.Fatalf("%+v", r)
	}
}

func TestDoFetchStopsWhenBudgetIsSpentAndCutsTimeout(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	callHost = func(string, any, any) error { t.Fatal("request started past the budget"); return nil }
	budgetEnd = now().Add(minCall / 2)
	if r := doFetch(fetchReq{}); r.Code != budgetCode {
		t.Fatalf("%+v", r)
	}
	var timeout int
	callHost = func(fn string, a, out any) error {
		timeout = a.(map[string]any)["timeout_ms"].(int)
		return nil
	}
	budgetEnd = now().Add(2 * time.Second)
	doFetch(fetchReq{})
	if timeout != 2000 {
		t.Fatalf("timeout %d", timeout)
	}
}

func TestTargetURLs(t *testing.T) {
	h := &fakeHost{settings: allSettings}
	h.install(t)
	c, _ := loadCfg()
	want := map[string]string{chanSlack: sampleSlack, chanTeams: sampleTeams, chanGChat: sampleGChat,
		chanTelegram: "https://api.telegram.org/bot" + sampleToken + "/sendMessage"}
	for ch, u := range want {
		if got, ok := target(c, ch); !ok || got != u {
			t.Errorf("%s: %q %v", ch, got, ok)
		}
	}
	if _, ok := target(cfg{}, chanSlack); ok {
		t.Fatal("unset channel has a target")
	}
}

func TestOnRetryExpiresPrunesThenDrains(t *testing.T) {
	h := &fakeHost{settings: allSettings, query: "[]"}
	h.install(t)
	if err := onRetry(sdk.CronEvent{Name: "retry"}); err != nil {
		t.Fatal(err)
	}
	var order []string
	for _, c := range h.calls {
		switch {
		case strings.HasPrefix(c.sql, "WITH d AS"):
			order = append(order, "expire")
		case strings.HasPrefix(c.sql, "DELETE FROM log"):
			order = append(order, "prune")
		case c.fn == "db.query":
			order = append(order, "claim")
		}
	}
	if strings.Join(order, ",") != "expire,prune,claim" {
		t.Fatalf("order %v", order)
	}
}

func TestCronsNeverFailOnHostErrors(t *testing.T) {
	callHost = func(string, any, any) error { return errors.New("host down") }
	t.Cleanup(func() { budgetEnd = time.Time{} })
	if onDeliver(sdk.CronEvent{}) != nil || onRetry(sdk.CronEvent{}) != nil {
		t.Fatal("cron returned an error")
	}
}
