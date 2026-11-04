package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// world fakes the host for the webhook: settings, Stripe, a payments table and
// the mail and log sinks. Everything the plugin sends out is recorded in out.
type world struct {
	settings    string
	settingsErr error
	stripe      map[string][2]string
	stripeErr   error
	dbErr       error
	mailErr     error

	events   map[string]bool // stored event ids
	sessions map[string]bool // stored session ids
	fetched  []string
	mails    []map[string]any
	out      strings.Builder // log lines, mails and responses
}

const goodSettings = `{"secret_key":"` + testKey + `","notify":true,"notify_email":"owner@example.com","links":[{"label":"Donate","url":"https://buy.stripe.com/abc","kind":"donation"}]}`

func newWorld(t *testing.T) *world {
	t.Helper()
	w := &world{
		settings: goodSettings,
		stripe: map[string][2]string{
			"https://api.stripe.com/v1/events/evt_1":          {"200", eventBody},
			"https://api.stripe.com/v1/payment_links/plink_1": {"200", `{"url":"https://buy.stripe.com/abc"}`},
		},
		events: map[string]bool{}, sessions: map[string]bool{},
	}
	callHost = func(fn string, a, out any) error {
		switch fn {
		case "settings.get":
			if w.settingsErr != nil {
				return w.settingsErr
			}
			return json.Unmarshal([]byte(w.settings), out)
		case "http.fetch":
			if w.stripeErr != nil {
				return w.stripeErr
			}
			url := a.(map[string]any)["url"].(string)
			w.fetched = append(w.fetched, url)
			ans, ok := w.stripe[url]
			if !ok {
				ans = [2]string{"404", `{"error":{"message":"No such event ` + testKey + `"}}`}
			}
			b, _ := json.Marshal(map[string]any{"status": atoi(ans[0]), "body": ans[1]})
			return json.Unmarshal(b, out)
		case "db.query":
			if w.dbErr != nil {
				return w.dbErr
			}
			m := a.(map[string]any)
			seen := w.events[m["args"].([]any)[0].(string)]
			return json.Unmarshal([]byte(`{"rows":[[{"seen":`+map[bool]string{true: "true", false: "false"}[seen]+`}]]}`), out)
		case "db.exec":
			if w.dbErr != nil {
				return w.dbErr
			}
			args := a.(map[string]any)["args"].([]any)
			ev, sess := args[0].(string), args[1].(string)
			n := 1
			if w.events[ev] || w.sessions[sess] {
				n = 0
			} else {
				w.events[ev], w.sessions[sess] = true, true
			}
			return json.Unmarshal([]byte(`{"rows_affected":`+itoa(int64(n))+`}`), out)
		case "mail.send":
			if w.mailErr != nil {
				return w.mailErr
			}
			m := a.(map[string]any)
			w.mails = append(w.mails, m)
			b, _ := json.Marshal(m)
			w.out.Write(b)
			return nil
		case "log":
			w.out.WriteString(a.(map[string]any)["message"].(string) + "\n")
			return nil
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	return w
}

func post(t *testing.T, w *world, body string) (int, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: "POST", Pattern: "/webhook", Body: body, Headers: map[string]string{"content-type": "application/json"}})
	if err != nil {
		t.Fatalf("handler returned a Go error: %v", err)
	}
	w.out.WriteString(res.Body)
	return res.Status, res.Body
}

func wantStatus(t *testing.T, w *world, body string, code int, state string) {
	t.Helper()
	got, resp := post(t, w, body)
	if got != code || (state != "" && !strings.Contains(resp, `"`+state+`"`)) {
		t.Fatalf("got %d %s, want %d %s", got, resp, code, state)
	}
}

const postedEvent = `{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"amount_total":999999}}}`

func TestWebhookRecordsAConfirmedPaymentAndMailsTheOwner(t *testing.T) {
	w := newWorld(t)
	wantStatus(t, w, postedEvent, 200, "recorded")
	if len(w.mails) != 1 {
		t.Fatalf("%d mails", len(w.mails))
	}
	m := w.mails[0]
	body := m["text"].(string)
	if m["to"].([]string)[0] != "owner@example.com" || m["subject"] != "New payment: Donate AUD 50.00" {
		t.Fatalf("mail %v", m)
	}
	for _, want := range []string{"Link: Donate", "Kind: donation", "Amount: AUD 50.00", "a@example.com", "cs_1", "2024-10-27T"} {
		if !strings.Contains(body, want) {
			t.Errorf("mail lacks %q:\n%s", want, body)
		}
	}
	if !w.events["evt_1"] {
		t.Fatal("event not stored")
	}
}

func TestWebhookReadsOnlyTheEventIDFromTheBody(t *testing.T) {
	w := newWorld(t)
	wantStatus(t, w, postedEvent, 200, "recorded")
	if strings.Contains(w.mails[0]["text"].(string), "999999") {
		t.Fatal("posted amount leaked into the mail")
	}
}

func TestWebhookIgnoresAnEventStripeDoesNotKnow(t *testing.T) {
	w := newWorld(t)
	wantStatus(t, w, `{"id":"evt_forged"}`, 200, "ignored")
	if len(w.events) != 0 || len(w.mails) != 0 {
		t.Fatal("forged event recorded")
	}
}

func TestWebhookDuplicateEventDoesNotCallStripe(t *testing.T) {
	w := newWorld(t)
	w.events["evt_1"] = true
	wantStatus(t, w, postedEvent, 200, "duplicate")
	if len(w.fetched) != 0 || len(w.mails) != 0 {
		t.Fatalf("fetched %v, mails %d", w.fetched, len(w.mails))
	}
}

func TestWebhookSecondEventForTheSameSessionSendsNoMail(t *testing.T) {
	w := newWorld(t)
	wantStatus(t, w, postedEvent, 200, "recorded")
	w.stripe["https://api.stripe.com/v1/events/evt_2"] = [2]string{"200", strings.Replace(
		strings.Replace(eventBody, `"evt_1"`, `"evt_2"`, 1), "checkout.session.completed", "checkout.session.async_payment_succeeded", 1)}
	wantStatus(t, w, `{"id":"evt_2"}`, 200, "duplicate")
	if len(w.mails) != 1 {
		t.Fatalf("%d mails", len(w.mails))
	}
}

func TestWebhookIgnoresEventsItDoesNotRecord(t *testing.T) {
	cases := map[string]string{
		"wrong type":       strings.Replace(eventBody, "checkout.session.completed", "charge.refunded", 1),
		"unpaid":           strings.Replace(eventBody, `"paid"`, `"unpaid"`, 1),
		"no payment link":  strings.Replace(eventBody, `"payment_link":"plink_1"`, `"payment_link":null`, 1),
		"bad link id":      strings.Replace(eventBody, `"plink_1"`, `"plink_../x"`, 1),
		"id mismatch":      strings.Replace(eventBody, `"id":"evt_1"`, `"id":"evt_9"`, 1),
		"currency missing": strings.Replace(eventBody, `"currency":"aud"`, `"currency":""`, 1),
	}
	for name, body := range cases {
		w := newWorld(t)
		w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"200", body}
		got, resp := post(t, w, postedEvent)
		if got != 200 || !strings.Contains(resp, "ignored") || len(w.events) != 0 || len(w.mails) != 0 {
			t.Errorf("%s: %d %s", name, got, resp)
		}
	}
}

func TestWebhookAsyncPaymentSucceededIsRecorded(t *testing.T) {
	w := newWorld(t)
	body := strings.Replace(strings.Replace(eventBody, "checkout.session.completed", "checkout.session.async_payment_succeeded", 1), `"paid"`, `"unpaid"`, 1)
	w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"200", body}
	wantStatus(t, w, postedEvent, 200, "recorded")
}

func TestWebhookAnswers503WhenItCannotTellSoStripeRetries(t *testing.T) {
	for name, mod := range map[string]func(*world){
		"missing key":       func(w *world) { w.settings = `{"notify":true}` },
		"settings error":    func(w *world) { w.settingsErr = &sdk.HostError{Code: sdk.ErrUnavailable} },
		"db error":          func(w *world) { w.dbErr = &sdk.HostError{Code: sdk.ErrUnavailable} },
		"stripe blocked":    func(w *world) { w.stripeErr = &sdk.HostError{Code: sdk.ErrBlocked} },
		"stripe 401":        func(w *world) { w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"401", `{}`} },
		"stripe bad json":   func(w *world) { w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"200", `nope`} },
		"stripe rate limit": func(w *world) { w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"429", `{}`} },
	} {
		w := newWorld(t)
		mod(w)
		got, resp := post(t, w, postedEvent)
		if got != 503 || strings.Contains(resp, "stripe") {
			t.Errorf("%s: %d %s", name, got, resp)
		}
		if len(w.mails) != 0 {
			t.Errorf("%s: mail sent", name)
		}
	}
}

func TestWebhookRejectsBadRequests(t *testing.T) {
	w := newWorld(t)
	for name, req := range map[string]sdk.HTTPRequest{
		"not json":        {Body: `nope`},
		"empty":           {Body: ``},
		"no id":           {Body: `{}`},
		"path in id":      {Body: `{"id":"evt_../x"}`},
		"other prefix":    {Body: `{"id":"cs_1"}`},
		"id not a string": {Body: `{"id":5}`},
		"too big":         {Body: `{"id":"evt_1","pad":"` + strings.Repeat("a", maxWebhookBody) + `"}`},
		"base64":          {Body: `{"id":"evt_1"}`, BodyB64: "eyJpZCI6ImV2dF8xIn0="},
		"wrong type":      {Body: `{"id":"evt_1"}`, Headers: map[string]string{"content-type": "text/plain"}},
	} {
		req.Method, req.Pattern = "POST", "/webhook"
		if req.Headers == nil {
			req.Headers = map[string]string{"content-type": "application/json"}
		}
		res, err := serve(req)
		if err != nil || res.Status != http.StatusBadRequest {
			t.Errorf("%s: %d %v", name, res.Status, err)
		}
	}
	if len(w.fetched) != 0 {
		t.Fatal("bad requests reached Stripe")
	}
}

func TestWebhookMailFailureStillRecords(t *testing.T) {
	w := newWorld(t)
	w.mailErr = &sdk.HostError{Code: sdk.ErrRateLimited, Message: "too many " + testKey}
	wantStatus(t, w, postedEvent, 200, "recorded")
	if !w.events["evt_1"] || !strings.Contains(w.out.String(), "owner email failed: rate_limited") {
		t.Fatalf("recorded=%v log=%q", w.events["evt_1"], w.out.String())
	}
}

func TestWebhookNoMailWhenNotifyOffOrNoAddress(t *testing.T) {
	for _, s := range []string{
		`{"secret_key":"` + testKey + `","notify":false,"notify_email":"owner@example.com"}`,
		`{"secret_key":"` + testKey + `","notify":true}`,
	} {
		w := newWorld(t)
		w.settings = s
		wantStatus(t, w, postedEvent, 200, "recorded")
		if len(w.mails) != 0 {
			t.Errorf("mail sent for %s", s)
		}
	}
}

func TestWebhookLabelFallsBackToTheLinkID(t *testing.T) {
	w := newWorld(t)
	delete(w.stripe, "https://api.stripe.com/v1/payment_links/plink_1")
	wantStatus(t, w, postedEvent, 200, "recorded")
	if !strings.Contains(w.mails[0]["text"].(string), "Link: plink_1") || !strings.Contains(w.mails[0]["text"].(string), "Kind: payment") {
		t.Fatalf("mail %v", w.mails[0])
	}
}

func TestMailSubjectIsOneLine(t *testing.T) {
	w := newWorld(t)
	w.settings = `{"secret_key":"` + testKey + `","notify_email":"owner@example.com","links":[{"label":"Don\r\nBcc: x@example.com","url":"https://buy.stripe.com/abc"}]}`
	wantStatus(t, w, postedEvent, 200, "recorded")
	if sub := w.mails[0]["subject"].(string); strings.ContainsAny(sub, "\r\n") {
		t.Fatalf("subject %q", sub)
	}
}

// Whatever goes wrong, the key never shows up in a log line, mail or response.
func TestSecretKeyNeverLeaves(t *testing.T) {
	for name, mod := range map[string]func(*world){
		"ok":             func(w *world) {},
		"stripe 404":     func(w *world) { delete(w.stripe, "https://api.stripe.com/v1/events/evt_1") },
		"stripe blocked": func(w *world) { w.stripeErr = &sdk.HostError{Code: sdk.ErrBlocked, Message: testKey} },
		"mail failed":    func(w *world) { w.mailErr = &sdk.HostError{Code: sdk.ErrUnavailable, Message: testKey} },
		"db error":       func(w *world) { w.dbErr = &sdk.HostError{Code: sdk.ErrInternal, Message: testKey} },
		"bad json": func(w *world) {
			w.stripe["https://api.stripe.com/v1/events/evt_1"] = [2]string{"200", "bad " + testKey}
		},
	} {
		w := newWorld(t)
		mod(w)
		post(t, w, postedEvent)
		if strings.Contains(w.out.String(), testKey) {
			t.Errorf("%s: key leaked: %s", name, w.out.String())
		}
	}
}
