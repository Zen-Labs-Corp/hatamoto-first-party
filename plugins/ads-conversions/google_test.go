package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

const subID = "7f3c1d52-0000-4000-8000-00000000a91e"

// sameJSON compares a request body with the expected document, ignoring key order.
func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("body is not JSON: %v: %s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("body = %s\nwant   %s", got, want)
	}
}

func loaded(t *testing.T, extra ...string) settings {
	h := newHost(t)
	h.configure(extra...)
	s, err := loadSettings()
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func googleJob() job {
	return job{SubmissionID: subID, Platform: platGoogle, EmHash: "emhash", EmGoogleHash: "emgoogle", PhHash: "phhash",
		IDs: clickIDs{"gclid": "G1"}, SubmittedAt: testNow}
}

func TestBuildGoogleGolden(t *testing.T) {
	s := loaded(t, `"google_login_customer_id":"111-222-3333"`, `"value":12.5`, `"currency":"EUR"`)
	j := googleJob()
	j.Consent = true
	req := buildGoogle(s, j, "access-tok", false)
	if req.Method != "POST" || req.URL != "https://datamanager.googleapis.com/v1/events:ingest" {
		t.Fatalf("req = %s %s", req.Method, req.URL)
	}
	if req.Headers["Authorization"] != "Bearer access-tok" || req.Headers["Content-Type"] != "application/json" {
		t.Fatalf("headers = %v", req.Headers)
	}
	sameJSON(t, req.Body, `{
	  "destinations":[{"operatingAccount":{"accountType":"GOOGLE_ADS","accountId":"1234567890"},
	    "loginAccount":{"accountType":"GOOGLE_ADS","accountId":"1112223333"},"productDestinationId":"987"}],
	  "encoding":"HEX","consent":{"adUserData":"CONSENT_GRANTED"},
	  "events":[{"transactionId":"`+subID+`","eventTimestamp":"2026-11-05T10:00:00Z","eventSource":"WEB",
	    "userData":{"userIdentifiers":[{"emailAddress":"emgoogle"},{"phoneNumber":"phhash"}]},
	    "adIdentifiers":{"gclid":"G1"},"conversionValue":12.5,"currency":"EUR"}]}`)
}

func TestBuildGoogleHashesOnlyNoLoginNoConsent(t *testing.T) {
	s := loaded(t)
	j := googleJob()
	j.IDs = nil
	j.PhHash = ""
	sameJSON(t, buildGoogle(s, j, "t", false).Body, `{
	  "destinations":[{"operatingAccount":{"accountType":"GOOGLE_ADS","accountId":"1234567890"},"productDestinationId":"987"}],
	  "encoding":"HEX",
	  "events":[{"transactionId":"`+subID+`","eventTimestamp":"2026-11-05T10:00:00Z","eventSource":"WEB",
	    "userData":{"userIdentifiers":[{"emailAddress":"emgoogle"}]}}]}`)
}

func TestBuildGoogleUsesGoogleEmailHashAndOneClickID(t *testing.T) {
	s := loaded(t)
	j := googleJob()
	j.IDs = clickIDs{"wbraid": "wb-id", "gbraid": "gb-id", "ttclid": "tt-id", "li_fat_id": "li-id"}
	body := string(buildGoogle(s, j, "t", false).Body)
	if strings.Contains(body, `"emhash"`) || !strings.Contains(body, `"adIdentifiers":{"gbraid":"gb-id"}`) ||
		strings.Contains(body, "wb-id") || strings.Contains(body, "tt-id") || strings.Contains(body, "li-id") {
		t.Fatalf("body = %s", body)
	}
}

func TestBuildGoogleValidateOnlyAndTokenOnlyInHeader(t *testing.T) {
	s := loaded(t)
	req := buildGoogle(s, googleJob(), "access-tok", true)
	var m map[string]any
	if err := json.Unmarshal(req.Body, &m); err != nil || m["validateOnly"] != true {
		t.Fatalf("body = %s", req.Body)
	}
	if strings.Contains(string(req.Body)+req.URL, "access-tok") {
		t.Fatal("token outside the header")
	}
	var real map[string]any
	if err := json.Unmarshal(buildGoogle(s, googleJob(), "t", false).Body, &real); err != nil {
		t.Fatal(err)
	}
	if _, ok := real["validateOnly"]; ok {
		t.Fatal("validateOnly set for a real send")
	}
}

func TestBuildGoogleEventIDIsSubmissionID(t *testing.T) {
	var b struct {
		Events []struct {
			TransactionID string `json:"transactionId"`
		} `json:"events"`
	}
	if err := json.Unmarshal(buildGoogle(loaded(t), googleJob(), "t", false).Body, &b); err != nil || len(b.Events) != 1 || b.Events[0].TransactionID != subID {
		t.Fatalf("events = %+v err %v", b, err)
	}
}

func TestHasIdentifierGoogle(t *testing.T) {
	cases := map[string]job{
		"email":  {EmGoogleHash: "h"},
		"phone":  {PhHash: "h"},
		"gclid":  {IDs: clickIDs{"gclid": "G"}},
		"gbraid": {IDs: clickIDs{"gbraid": "G"}},
		"wbraid": {IDs: clickIDs{"wbraid": "G"}},
	}
	for name, j := range cases {
		if !hasIdentifierGoogle(j) {
			t.Errorf("%s should count", name)
		}
	}
	if hasIdentifierGoogle(job{EmHash: "h", IDs: clickIDs{"ttclid": "T", "li_fat_id": "L"}}) {
		t.Error("TikTok and LinkedIn IDs must not count for Google")
	}
}

func TestClassifyGoogle(t *testing.T) {
	long := strings.Repeat("m", 500)
	cases := []struct {
		status int
		body   string
		kind   string
		msg    string
	}{
		{200, `{}`, kindSent, ""},
		{200, `{"requestId":"r"}`, kindSent, ""},
		{401, `{"error":{"code":401,"message":"invalid token"}}`, kindAuth, "invalid token"},
		{429, `{"error":{"message":"slow down"}}`, kindRetry, "slow down"},
		{500, ``, kindRetry, ""},
		{503, `<html>`, kindRetry, ""},
		{400, `{"error":{"message":"bad conversion action"}}`, kindFailed, "bad conversion action"},
		{403, `{"error":{"message":"` + long + `"}}`, kindFailed, long[:maxMessage]},
		{404, `not json`, kindFailed, ""},
	}
	for _, c := range cases {
		o := classifyGoogle(c.status, []byte(c.body))
		if o.Kind != c.kind || o.Message != c.msg {
			t.Errorf("classifyGoogle(%d) = %+v, want %s %q", c.status, o, c.kind, c.msg)
		}
	}
}
