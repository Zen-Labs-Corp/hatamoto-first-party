package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func linkedInJob() job {
	return job{SubmissionID: subID, Platform: platLinkedIn, EmHash: "emhash", PhHash: "phhash",
		IDs: clickIDs{"li_fat_id": "LI1", "gclid": "G1"}, SubmittedAt: testNow}
}

func TestBuildLinkedInGolden(t *testing.T) {
	s := loaded(t, `"linkedin_ad_account_id":"512345"`, `"value":12.5`, `"currency":"EUR"`)
	req := buildLinkedIn(s, linkedInJob(), "li-token")
	if req.Method != "POST" || req.URL != "https://api.linkedin.com/rest/conversionEvents" {
		t.Fatalf("req = %s %s", req.Method, req.URL)
	}
	h := req.Headers
	if h["Authorization"] != "Bearer li-token" || h["LinkedIn-Version"] != "202610" || h["X-Restli-Protocol-Version"] != "2.0.0" || h["Content-Type"] != "application/json" {
		t.Fatalf("headers = %v", h)
	}
	sameJSON(t, req.Body, `{"conversion":"urn:lla:llaPartnerConversion:5","conversionHappenedAt":1793872800000,
	  "conversionValue":{"currencyCode":"EUR","amount":"12.5"},
	  "user":{"userIds":[{"idType":"SHA256_EMAIL","idValue":"emhash"},
	    {"idType":"LINKEDIN_FIRST_PARTY_ADS_TRACKING_UUID","idValue":"LI1"}]},
	  "eventId":"`+subID+`"}`)
}

func TestBuildLinkedInHashesOnlyNoValue(t *testing.T) {
	s := loaded(t, `"linkedin_version":"202601"`, `"value":3`)
	j := linkedInJob()
	j.IDs = nil
	req := buildLinkedIn(s, j, "t")
	if req.Headers["LinkedIn-Version"] != "202601" {
		t.Fatalf("version = %q", req.Headers["LinkedIn-Version"])
	}
	// A value without a currency is not sent: LinkedIn needs both.
	sameJSON(t, req.Body, `{"conversion":"urn:lla:llaPartnerConversion:5","conversionHappenedAt":1793872800000,
	  "user":{"userIds":[{"idType":"SHA256_EMAIL","idValue":"emhash"}]},"eventId":"`+subID+`"}`)
}

func TestBuildLinkedInSendsNoPhoneAndNoOtherIDs(t *testing.T) {
	body := string(buildLinkedIn(loaded(t), linkedInJob(), "li-token").Body)
	for _, bad := range []string{"phhash", "G1", "li-token", "phone", "firstName", "lastName", "userInfo"} {
		if strings.Contains(body, bad) {
			t.Fatalf("body carries %q: %s", bad, body)
		}
	}
	var b struct {
		EventID string `json:"eventId"`
	}
	if err := json.Unmarshal([]byte(body), &b); err != nil || b.EventID != subID {
		t.Fatalf("eventId = %q", b.EventID)
	}
	j := linkedInJob()
	j.EmHash = ""
	if !strings.Contains(string(buildLinkedIn(loaded(t), j, "t").Body), `"idType":"LINKEDIN_FIRST_PARTY_ADS_TRACKING_UUID"`) {
		t.Fatal("li_fat_id alone must be sent")
	}
}

func TestBuildLinkedInCheck(t *testing.T) {
	s := loaded(t, `"linkedin_ad_account_id":"512345"`)
	req := buildLinkedInCheck(s, "li-token")
	if req.Method != "GET" || req.URL != "https://api.linkedin.com/rest/conversions/5?account=urn%3Ali%3AsponsoredAccount%3A512345" ||
		len(req.Body) != 0 || req.Headers["Authorization"] != "Bearer li-token" || req.Headers["LinkedIn-Version"] != "202610" {
		t.Fatalf("req = %+v", req)
	}
}

func TestHasIdentifierLinkedIn(t *testing.T) {
	if !hasIdentifierLinkedIn(job{EmHash: "h"}) || !hasIdentifierLinkedIn(job{IDs: clickIDs{"li_fat_id": "L"}}) {
		t.Error("email or li_fat_id must count")
	}
	if hasIdentifierLinkedIn(job{PhHash: "h", EmGoogleHash: "h", IDs: clickIDs{"gclid": "G", "ttclid": "T"}}) {
		t.Error("phone and other platforms' IDs must not count")
	}
}

func TestClassifyLinkedIn(t *testing.T) {
	cases := []struct {
		status int
		body   string
		kind   string
		msg    string
	}{
		{201, ``, kindSent, ""},
		{401, `{"status":401,"message":"The token used in the request has expired"}`, kindAuth, "The token used in the request has expired"},
		{429, `{"message":"Too many requests"}`, kindRetry, "Too many requests"},
		{502, ``, kindRetry, ""},
		{400, `{"message":"Invalid conversion"}`, kindFailed, "Invalid conversion"},
		{403, `not json`, kindFailed, ""},
		{422, `{"message":"` + strings.Repeat("x", 400) + `"}`, kindFailed, strings.Repeat("x", maxMessage)},
	}
	for _, c := range cases {
		o := classifyLinkedIn(c.status, []byte(c.body))
		if o.Kind != c.kind || o.Message != c.msg {
			t.Errorf("classifyLinkedIn(%d) = %+v, want %s %q", c.status, o, c.kind, c.msg)
		}
	}
}
