package main

import (
	"net/url"
	"strings"
	"testing"
	"time"
)

const tpUnitFixture = `{"displayName":"Acme","name":{"identifying":"www.acme.example"},"numberOfReviews":{"total":210},"score":{"stars":4.5,"trustScore":4.6}}`
const tpReviewsFixture = `{"reviews":[
{"id":"r1","stars":5,"title":"Great","text":"Fast service","createdAt":"2026-10-21T08:00:00Z","consumer":{"displayName":"Sam P."}},
{"id":"r2","stars":4,"title":"","text":"Fine","createdAt":"2026-10-20T08:00:00Z","consumer":{"displayName":"Ali"}},
{"id":"r3","stars":4,"title":"Nice!","text":"","createdAt":"2026-10-19T08:00:00Z","consumer":{"displayName":"Bo"}},
{"id":"r4","stars":0,"createdAt":"2026-10-19T08:00:00Z","consumer":{"displayName":"X"}},
{"id":"","stars":5,"createdAt":"2026-10-19T08:00:00Z","consumer":{"displayName":"Y"}},
{"id":"r6","stars":5,"createdAt":"2026-10-19T08:00:00Z","consumer":{}}
]}`

func tpSettings() settings {
	return settings{TPKey: "T-SECRET", TPUnit: "UNIT1", MinRating: 4, MaxItems: 6}
}

func tpFake(f *fakeHost) {
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		if strings.Contains(u, "/reviews?") {
			return 200, tpReviewsFixture, nil
		}
		return 200, tpUnitFixture, nil
	}
}

func TestFetchTrustpilotRequestsAndMapping(t *testing.T) {
	f := newFake(t)
	tpFake(f)
	info, rs, err := fetchTrustpilot(tpSettings(), f.clock.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	if len(f.requests) != 2 {
		t.Fatalf("requests %+v", f.requests)
	}
	u0, _ := url.Parse(f.requests[0].URL)
	if u0.Host != "api.trustpilot.com" || u0.Path != "/v1/business-units/UNIT1" || u0.Query().Get("apikey") != "T-SECRET" {
		t.Fatalf("unit request %s", f.requests[0].URL)
	}
	u1, _ := url.Parse(f.requests[1].URL)
	q := u1.Query()
	if u1.Path != "/v1/business-units/UNIT1/reviews" || q.Get("perPage") != "20" || q.Get("orderBy") != "createdat.desc" || q.Get("apikey") != "T-SECRET" {
		t.Fatalf("reviews request %s", f.requests[1].URL)
	}
	if info.Rating != 4.6 || info.Count != 210 || info.Name != "Acme" || info.ProfileURL != "https://www.trustpilot.com/review/www.acme.example" {
		t.Fatalf("info %+v", info)
	}
	if len(rs) != 3 {
		t.Fatalf("reviews %+v", rs)
	}
	if rs[0].Body != "Great. Fast service" || rs[1].Body != "Fine" || rs[2].Body != "Nice!" ||
		rs[0].Link != "https://www.trustpilot.com/reviews/r1" || rs[0].Author != "Sam P." || rs[0].Rating != 5 {
		t.Fatalf("mapped %+v", rs)
	}
}

func TestTrustpilotScoreFallsBackToStars(t *testing.T) {
	f := newFake(t)
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		if strings.Contains(u, "/reviews?") {
			return 200, `{}`, nil
		}
		return 200, `{"score":{"stars":4.5}}`, nil
	}
	info, rs, err := fetchTrustpilot(tpSettings(), f.clock.Add(9*time.Second))
	if err != nil || info.Rating != 4.5 || info.ProfileURL != "" || len(rs) != 0 {
		t.Fatalf("%+v %v %v", info, rs, err)
	}
}

func TestTrustpilotErrorsNeverCarryTheKey(t *testing.T) {
	f := newFake(t)
	dl := f.clock.Add(9 * time.Second)
	f.fetch = func(string, map[string]string) (int, string, error) {
		return 401, `{"message":"invalid apikey T-SECRET"}`, nil
	}
	_, _, err := fetchTrustpilot(tpSettings(), dl)
	if err == nil || strings.Contains(err.Error(), "T-SECRET") || !strings.Contains(err.Error(), "401") {
		t.Fatalf("401 error %v", err)
	}
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		return 0, "", &testErr{"get " + u + " timed out"}
	}
	_, _, err = fetchTrustpilot(tpSettings(), dl)
	if err == nil || strings.Contains(err.Error(), "T-SECRET") {
		t.Fatalf("host error %v", err)
	}
}

func TestRedactEscapedForms(t *testing.T) {
	got := redact("a k/e y b k%2Fe+y c", "k/e y", "")
	if strings.Contains(got, "k/e") || strings.Contains(got, "k%2F") {
		t.Fatal(got)
	}
}
