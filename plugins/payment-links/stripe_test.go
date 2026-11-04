package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const testKey = "rk_test_SECRET123"

type fetchCall struct {
	url, auth string
}

// stripeHost fakes http.fetch: answers maps a URL to a status and body.
func stripeHost(t *testing.T, answers map[string][2]string, hostErr error, truncated bool) *[]fetchCall {
	t.Helper()
	var calls []fetchCall
	callHost = func(fn string, a, out any) error {
		if fn != "http.fetch" {
			t.Fatalf("unexpected host call %q", fn)
		}
		m := a.(map[string]any)
		url := m["url"].(string)
		calls = append(calls, fetchCall{url, m["headers"].(map[string]string)["Authorization"]})
		if m["method"] != "GET" {
			t.Errorf("method %v", m["method"])
		}
		if hostErr != nil {
			return hostErr
		}
		ans, ok := answers[url]
		if !ok {
			ans = [2]string{"404", `{"error":{"message":"No such thing ` + testKey + `"}}`}
		}
		b, _ := json.Marshal(map[string]any{"status": atoi(ans[0]), "body": ans[1], "truncated": truncated})
		return json.Unmarshal(b, out)
	}
	return &calls
}

func atoi(s string) int { var n int; _ = json.Unmarshal([]byte(s), &n); return n }

const eventBody = `{"id":"evt_1","type":"checkout.session.completed","data":{"object":{"id":"cs_1","payment_status":"paid","currency":"aud","payment_link":"plink_1","amount_total":5000,"created":1730000000,"livemode":true,"customer_details":{"email":"a@example.com"}}}}`

func TestFetchEventSendsAuthorizedGetToStripe(t *testing.T) {
	calls := stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/events/evt_1": {"200", eventBody}}, nil, false)
	ev, status, err := fetchEvent(testKey, "evt_1")
	if err != nil || status != 200 {
		t.Fatalf("%d %v", status, err)
	}
	if c := (*calls)[0]; c.url != "https://api.stripe.com/v1/events/evt_1" || c.auth != "Bearer "+testKey {
		t.Fatalf("call %+v", c)
	}
	s := ev.Data.Object
	if ev.Type != "checkout.session.completed" || s.ID != "cs_1" || s.AmountTotal != 5000 || s.PaymentLink != "plink_1" || s.CustomerDetails.Email != "a@example.com" || !s.Livemode {
		t.Fatalf("event %+v", ev)
	}
}

func TestFetchEventRejectsBadIDsWithoutCalling(t *testing.T) {
	calls := stripeHost(t, nil, nil, false)
	for _, id := range []string{"", "evt_", "evt_../x", "evt_a/b", "cs_1", "evt_a?x=1", "evt_" + strings.Repeat("a", 251)} {
		if _, _, err := fetchEvent(testKey, id); err == nil {
			t.Errorf("%q accepted", id)
		}
	}
	if len(*calls) != 0 {
		t.Fatalf("%d fetches", len(*calls))
	}
}

func TestStripeFailuresAreFixedStringsWithoutTheKey(t *testing.T) {
	stripeHost(t, nil, nil, false)
	status, err := stripeGet(testKey, "/v1/events/evt_1", nil)
	if err != nil || status != 404 {
		t.Fatalf("404: %d %v", status, err)
	}

	stripeHost(t, nil, &sdk.HostError{Code: sdk.ErrBlocked, Message: "host " + testKey}, false)
	_, err = stripeGet(testKey, "/v1/events/evt_1", nil)
	if err == nil || strings.Contains(err.Error(), testKey) || err != errStripeCall {
		t.Fatalf("host error: %v", err)
	}

	stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/x": {"200", `{}`}}, nil, true)
	if _, err = stripeGet(testKey, "/v1/x", nil); err != errStripeTooLarge {
		t.Fatalf("truncated: %v", err)
	}

	stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/x": {"200", `not json ` + testKey}}, nil, false)
	var out map[string]any
	if _, err = stripeGet(testKey, "/v1/x", &out); err != errStripeBadJSON || strings.Contains(err.Error(), testKey) {
		t.Fatalf("bad json: %v", err)
	}
}

func TestHostCodeNamesOnlyTheCode(t *testing.T) {
	if got := hostCode(&sdk.HostError{Code: sdk.ErrRateLimited, Message: testKey}); got != sdk.ErrRateLimited {
		t.Fatalf("code %q", got)
	}
	if got := hostCode(errors.New(testKey)); got != "error" {
		t.Fatalf("code %q", got)
	}
}

func TestResolveLinkMatchesTheSettingsURL(t *testing.T) {
	links := []link{
		{Label: "Consult", URL: "https://buy.stripe.com/other", Kind: "payment"},
		{Label: "Donate", URL: "https://buy.stripe.com/abc/?utm=x", Kind: "donation"},
	}
	stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/payment_links/plink_1": {"200", `{"url":"https://buy.stripe.com/abc#top"}`}}, nil, false)
	if l, k := resolveLink(testKey, "plink_1", links); l != "Donate" || k != "donation" {
		t.Fatalf("match: %q %q", l, k)
	}
}

func TestResolveLinkFallsBackToTheLinkID(t *testing.T) {
	links := []link{{Label: "Donate", URL: "https://buy.stripe.com/abc", Kind: "donation"}}
	cases := map[string]func(){
		"no match": func() {
			stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/payment_links/plink_1": {"200", `{"url":"https://buy.stripe.com/zzz"}`}}, nil, false)
		},
		"not found": func() { stripeHost(t, nil, nil, false) },
		"host error": func() {
			stripeHost(t, nil, &sdk.HostError{Code: sdk.ErrBlocked}, false)
		},
		"bad json": func() {
			stripeHost(t, map[string][2]string{"https://api.stripe.com/v1/payment_links/plink_1": {"200", `nope`}}, nil, false)
		},
	}
	for name, setup := range cases {
		setup()
		if l, k := resolveLink(testKey, "plink_1", links); l != "plink_1" || k != "payment" {
			t.Errorf("%s: %q %q", name, l, k)
		}
	}
	calls := stripeHost(t, nil, nil, false)
	if l, k := resolveLink(testKey, "plink_../x", links); l != "plink_../x" || k != "payment" || len(*calls) != 0 {
		t.Errorf("bad id: %q %q, %d calls", l, k, len(*calls))
	}
}
