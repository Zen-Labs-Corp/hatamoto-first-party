package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// Placeholder keys: obviously fake, shaped to pass the setting patterns.
const (
	testStripeKey    = "sk_test_fakekey001"
	testStripeSecret = "whsec_fakehook01"
)

// fetched is one http.fetch call the plugin made.
type fetched struct {
	method, url, body string
	headers           map[string]string
	timeoutMS         int
}

// provHost fakes http.fetch and the meta table for the provider clients: the
// answer of a call is looked up by "METHOD url", every call is recorded.
type provHost struct {
	answers map[string][2]string // "METHOD url" -> {status, body}
	netErr  error
	meta    map[string]string
	calls   []fetched
	dbCalls int
}

func newProvHost(t *testing.T) *provHost {
	t.Helper()
	h := &provHost{answers: map[string][2]string{}, meta: map[string]string{}}
	clock := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return clock }
	t.Cleanup(func() { now = time.Now })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		switch fn {
		case sdk.FnHTTPFetch:
			f := fetched{method: a["method"].(string), url: a["url"].(string), headers: a["headers"].(map[string]string), timeoutMS: a["timeout_ms"].(int)}
			f.body, _ = a["body"].(string)
			h.calls = append(h.calls, f)
			if h.netErr != nil {
				return h.netErr
			}
			ans, ok := h.answers[f.method+" "+f.url]
			if !ok {
				ans = [2]string{"404", `{"error":"` + testStripeKey + `"}`}
			}
			return jsonInto(`{"status":`+ans[0]+`,"body":`+jsonText(ans[1])+`}`, out)
		case sdk.FnDBQuery:
			h.dbCalls++
			ar := a["args"].([]any)
			if v, ok := h.meta[ar[0].(string)]; ok {
				return jsonInto(`{"rows":[[`+jsonText(v)+`]]}`, out)
			}
			return jsonInto(`{"rows":[[null]]}`, out)
		case sdk.FnDBExec:
			h.dbCalls++
			ar := a["args"].([]any)
			h.meta[ar[0].(string)] = ar[1].(string)
			return nil
		}
		t.Errorf("unexpected host call %q", fn)
		return nil
	}
	return h
}

func (h *provHost) only(t *testing.T) fetched {
	t.Helper()
	if len(h.calls) != 1 {
		t.Fatalf("%d fetches, want 1", len(h.calls))
	}
	return h.calls[0]
}

func stripeSettings() settings {
	return settings{StripeKey: testStripeKey, StripeWebhookSecret: testStripeSecret}
}

func sign(secret, ts, body string) string {
	m := hmac.New(sha256.New, []byte(secret))
	m.Write([]byte(ts + "." + body))
	return hex.EncodeToString(m.Sum(nil))
}

func TestVerifyStripeSignature(t *testing.T) {
	at := time.Unix(1800000000, 0)
	ts := strconv.FormatInt(at.Unix(), 10)
	body := `{"id":"evt_1", "type":"checkout.session.completed"}` // whitespace matters: the raw body is signed
	good := sign(testStripeSecret, ts, body)
	other := sign("whsec_otherfake01", ts, body)
	cases := []struct {
		name, secret, header, body string
		at                         time.Time
		want                       bool
	}{
		{"valid", testStripeSecret, "t=" + ts + ",v1=" + good, body, at, true},
		{"one of several v1", testStripeSecret, "t=" + ts + ",v1=" + other + ",v1=" + good + ",v0=ab", body, at, true},
		{"spaces after commas", testStripeSecret, "t=" + ts + ", v1=" + good, body, at, true},
		{"edge of tolerance past", testStripeSecret, "t=" + ts + ",v1=" + good, body, at.Add(300 * time.Second), true},
		{"edge of tolerance future", testStripeSecret, "t=" + ts + ",v1=" + good, body, at.Add(-300 * time.Second), true},
		{"stale", testStripeSecret, "t=" + ts + ",v1=" + good, body, at.Add(301 * time.Second), false},
		{"too far ahead", testStripeSecret, "t=" + ts + ",v1=" + good, body, at.Add(-301 * time.Second), false},
		{"wrong secret", "whsec_otherfake01", "t=" + ts + ",v1=" + good, body, at, false},
		{"body changed", testStripeSecret, "t=" + ts + ",v1=" + good, strings.ReplaceAll(body, " ", ""), at, false},
		{"timestamp changed", testStripeSecret, "t=" + strconv.FormatInt(at.Unix()+1, 10) + ",v1=" + good, body, at, false},
		{"empty secret", "", "t=" + ts + ",v1=" + sign("", ts, body), body, at, false},
		{"empty header", testStripeSecret, "", body, at, false},
		{"no v1", testStripeSecret, "t=" + ts, body, at, false},
		{"only v0", testStripeSecret, "t=" + ts + ",v0=" + good, body, at, false},
		{"no t", testStripeSecret, "v1=" + good, body, at, false},
		{"two t", testStripeSecret, "t=" + ts + ",t=" + ts + ",v1=" + good, body, at, false},
		{"non-numeric t", testStripeSecret, "t=abc,v1=" + good, body, at, false},
		{"negative t", testStripeSecret, "t=-5,v1=" + good, body, at, false},
		{"v1 not hex", testStripeSecret, "t=" + ts + ",v1=zz" + good[2:], body, at, false},
		{"v1 truncated", testStripeSecret, "t=" + ts + ",v1=" + good[:62], body, at, false},
		{"v1 empty", testStripeSecret, "t=" + ts + ",v1=", body, at, false},
		{"nine v1", testStripeSecret, "t=" + ts + strings.Repeat(",v1="+other, 8) + ",v1=" + good, body, at, false},
		{"header too long", testStripeSecret, "t=" + ts + ",v1=" + good + ",x=" + strings.Repeat("a", 1024), body, at, false},
		{"garbage", testStripeSecret, "not a signature", body, at, false},
	}
	for _, c := range cases {
		if got := verifyStripeSignature(c.secret, c.header, c.body, c.at); got != c.want {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
	}
}

func TestStripeCreateSession(t *testing.T) {
	h := newProvHost(t)
	h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"https://checkout.stripe.com/c/pay/cs_test_1"}`}
	id, page, err := stripeCreateSession(stripeSettings(), payIn{
		OrderID: 42, PayCode: "paycode", Email: "ana@example.com", Title: "Tickets: Gig & Co", Currency: "USD", Total: 1250,
		SuccessURL: "https://example.org/p/event-tickets/return/tok?session_id={CHECKOUT_SESSION_ID}", CancelURL: "https://example.org/p/event-tickets/cancel/tok",
	})
	if err != nil || id != "cs_test_1" || page != "https://checkout.stripe.com/c/pay/cs_test_1" {
		t.Fatalf("%q %q %v", id, page, err)
	}
	c := h.only(t)
	if c.headers["Authorization"] != "Bearer "+testStripeKey || c.headers["Idempotency-Key"] != "et-42-create" ||
		c.headers["Content-Type"] != "application/x-www-form-urlencoded" {
		t.Fatalf("headers %v", c.headers)
	}
	if c.timeoutMS > 4000 || c.timeoutMS < 1 {
		t.Fatalf("timeout %d", c.timeoutMS)
	}
	form, err := url.ParseQuery(c.body)
	if err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		"mode": "payment", "line_items[0][quantity]": "1", "line_items[0][price_data][currency]": "usd",
		"line_items[0][price_data][unit_amount]":        "1250",
		"line_items[0][price_data][product_data][name]": "Tickets: Gig & Co",
		"client_reference_id":                           "paycode", "metadata[order_id]": "42", "customer_email": "ana@example.com",
		"success_url": "https://example.org/p/event-tickets/return/tok?session_id={CHECKOUT_SESSION_ID}",
		"cancel_url":  "https://example.org/p/event-tickets/cancel/tok",
		"expires_at":  strconv.FormatInt(now().Add(30*time.Minute).Unix(), 10),
	}
	for k, v := range want {
		if form.Get(k) != v {
			t.Errorf("%s = %q, want %q", k, form.Get(k), v)
		}
	}
	if len(form) != len(want) {
		t.Errorf("%d form fields, want %d: %v", len(form), len(want), form)
	}
}

func TestStripeCreateSessionZeroDecimalAmountIsWhole(t *testing.T) {
	h := newProvHost(t)
	h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = [2]string{"200", `{"id":"cs_test_1","url":"https://checkout.stripe.com/x"}`}
	if _, _, err := stripeCreateSession(stripeSettings(), payIn{OrderID: 1, Currency: "JPY", Total: 1200}); err != nil {
		t.Fatal(err)
	}
	form, _ := url.ParseQuery(h.only(t).body)
	if form.Get("line_items[0][price_data][unit_amount]") != "1200" || form.Get("line_items[0][price_data][currency]") != "jpy" {
		t.Fatalf("%v", form)
	}
}

func TestStripeGetExpireRefund(t *testing.T) {
	h := newProvHost(t)
	h.answers["GET https://api.stripe.com/v1/checkout/sessions/cs_test_1"] = [2]string{"200",
		`{"id":"cs_test_1","status":"complete","payment_status":"paid","amount_total":1250,"currency":"usd","payment_intent":"pi_1","client_reference_id":"paycode"}`}
	h.answers["POST https://api.stripe.com/v1/checkout/sessions/cs_test_1/expire"] = [2]string{"200", `{"id":"cs_test_1"}`}
	h.answers["POST https://api.stripe.com/v1/refunds"] = [2]string{"200", `{"id":"re_1"}`}
	s := stripeSettings()

	got, err := stripeGetSession(s, "cs_test_1")
	if err != nil || got.PaymentStatus != "paid" || got.AmountTotal != 1250 || got.Currency != "usd" || got.PaymentIntent != "pi_1" || got.ClientReference != "paycode" {
		t.Fatalf("%+v %v", got, err)
	}
	if err := stripeExpireSession(s, "cs_test_1"); err != nil {
		t.Fatal(err)
	}
	id, err := stripeRefund(s, "pi_1", 500, "et-42-refund-0-500")
	if err != nil || id != "re_1" {
		t.Fatalf("%q %v", id, err)
	}
	r := h.calls[2]
	form, _ := url.ParseQuery(r.body)
	if r.headers["Idempotency-Key"] != "et-42-refund-0-500" || form.Get("payment_intent") != "pi_1" || form.Get("amount") != "500" || len(form) != 2 {
		t.Fatalf("%v %v", r.headers, form)
	}
}

func TestStripeRefusesBadIDsBeforeTheNetwork(t *testing.T) {
	h := newProvHost(t)
	s := stripeSettings()
	for _, id := range []string{"", "cs/../../v1/charges", "cs?x=1", "cs 1", strings.Repeat("a", 256)} {
		if _, err := stripeGetSession(s, id); !errors.Is(err, errProviderRefused) {
			t.Errorf("get %q: %v", id, err)
		}
		if err := stripeExpireSession(s, id); !errors.Is(err, errProviderRefused) {
			t.Errorf("expire %q: %v", id, err)
		}
		if _, err := stripeRefund(s, id, 100, "k"); !errors.Is(err, errProviderRefused) {
			t.Errorf("refund %q: %v", id, err)
		}
	}
	if _, err := stripeRefund(s, "pi_1", 0, "k"); !errors.Is(err, errProviderRefused) {
		t.Errorf("refund of nothing: %v", err)
	}
	if _, err := stripeGetSession(settings{}, "cs_1"); !errors.Is(err, errProviderRefused) {
		t.Errorf("no key: %v", err)
	}
	if len(h.calls) != 0 {
		t.Fatalf("%d calls reached the host", len(h.calls))
	}
}

// Whatever Stripe or the host says, the error is a fixed string with neither
// the key nor anything Stripe sent.
func TestStripeErrorsAreFixedStrings(t *testing.T) {
	h := newProvHost(t)
	s := stripeSettings()
	secretBody := `{"error":{"message":"Invalid API Key provided: ` + testStripeKey + `"}}`
	cases := []struct {
		name   string
		answer [2]string
		netErr error
		want   error
	}{
		{"401", [2]string{"401", secretBody}, nil, errProviderRefused},
		{"402", [2]string{"402", secretBody}, nil, errProviderRefused},
		{"429", [2]string{"429", secretBody}, nil, errProviderDown},
		{"500", [2]string{"500", secretBody}, nil, errProviderDown},
		{"not json", [2]string{"200", "<html>" + testStripeKey}, nil, errProviderBadAnswer},
		{"no id", [2]string{"200", `{"url":"https://checkout.stripe.com/x"}`}, nil, errProviderBadAnswer},
		{"network", [2]string{}, errors.New("dial tcp: " + testStripeKey), errProviderDown},
		{"host error", [2]string{}, &sdk.HostError{Code: sdk.ErrNotFound, Message: testStripeKey}, errProviderDown},
	}
	for _, c := range cases {
		h.answers["POST https://api.stripe.com/v1/checkout/sessions"] = c.answer
		h.netErr = c.netErr
		_, _, err := stripeCreateSession(s, payIn{OrderID: 1, Currency: "USD", Total: 100})
		if !errors.Is(err, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, err, c.want)
			continue
		}
		if strings.Contains(err.Error(), testStripeKey) || strings.Contains(err.Error(), "sk_") {
			t.Errorf("%s: error carries the key: %v", c.name, err)
		}
	}
}

func TestTruncatedAnswerIsAnError(t *testing.T) {
	newProvHost(t)
	callHost = func(fn string, args, out any) error {
		return jsonInto(`{"status":200,"body":"{}","truncated":true}`, out)
	}
	if _, err := stripeGetSession(stripeSettings(), "cs_1"); !errors.Is(err, errProviderBadAnswer) {
		t.Fatalf("%v", err)
	}
}

func TestFetchHonoursTheDeadline(t *testing.T) {
	h := newProvHost(t)
	url := stripeAPI + "/v1/x"
	if _, _, err := fetchJSON("GET", url, nil, "", newDeadline(1000*time.Millisecond)); !errors.Is(err, errNoTime) {
		t.Fatalf("1000 ms left: %v", err)
	}
	if len(h.calls) != 0 {
		t.Fatal("a call was made with too little time left")
	}
	h.answers["GET "+url] = [2]string{"200", "{}"}
	if _, _, err := fetchJSON("GET", url, nil, "", newDeadline(2500*time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	if _, _, err := fetchJSON("GET", url, nil, "", newDeadline(9*time.Second)); err != nil {
		t.Fatal(err)
	}
	if h.calls[0].timeoutMS != 2500 || h.calls[1].timeoutMS != 4000 {
		t.Fatalf("timeouts %d %d", h.calls[0].timeoutMS, h.calls[1].timeoutMS)
	}
}

func TestFetchOnlyReachesTheProviderHosts(t *testing.T) {
	h := newProvHost(t)
	for _, u := range []string{
		"http://api.stripe.com/v1/x", "https://evil.example.org/v1/x", "https://api.stripe.com.evil.example.org/x",
		"https://api.stripe.com:8443/x", "https://user@api.stripe.com/x", "/v1/x", "",
	} {
		if _, _, err := fetchJSON("GET", u, nil, "", deadline{}); !errors.Is(err, errProviderRefused) {
			t.Errorf("%q: %v", u, err)
		}
	}
	if len(h.calls) != 0 {
		t.Fatalf("%d calls reached the host", len(h.calls))
	}
}

func TestCheckoutURLOK(t *testing.T) {
	for raw, want := range map[string]bool{
		"https://checkout.stripe.com/c/pay/cs_test_1#frag":     true,
		"https://www.paypal.com/checkoutnow?token=ABC":         true,
		"https://www.sandbox.paypal.com/checkoutnow?token=ABC": true,
		"http://checkout.stripe.com/c/pay":                     false,
		"https://checkout.stripe.com:444/c":                    false,
		"https://user:pw@checkout.stripe.com/c":                false,
		"https://checkout.stripe.com@evil.example.org/c":       false,
		"https://evil.example.org/checkout.stripe.com":         false,
		"https://checkout.stripe.com.evil.example.org/c":       false,
		"https://api.stripe.com/v1":                            false,
		"https://paypal.com/x":                                 false,
		"//checkout.stripe.com/c":                              false,
		"javascript:alert(1)":                                  false,
		"https:checkout.stripe.com":                            false,
		"/p/event-tickets/ticket/x":                            false,
		"":                                                     false,
		"https://checkout.stripe.com\\@evil.example.org":       false,
		"https://checkout.stripe.com%2f@evil.example.org/c":    false,
		" https://checkout.stripe.com/c":                       false,
	} {
		if got := checkoutURLOK(raw); got != want {
			t.Errorf("checkoutURLOK(%q) = %v, want %v", raw, got, want)
		}
	}
}
