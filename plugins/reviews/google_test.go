package main

import (
	"strings"
	"testing"
	"time"
)

const googleFixture = `{"displayName":{"text":"Acme"},"rating":4.8,"userRatingCount":132,"googleMapsUri":"https://maps.google.com/?cid=1",
"reviews":[
{"name":"places/P/reviews/r1","rating":5,"text":{"text":"Great"},"publishTime":"2026-10-30T10:11:12.5Z",
 "authorAttribution":{"displayName":"Jane D.","uri":"https://www.google.com/maps/contrib/1","photoUri":"https://lh3.example/p.jpg"},"googleMapsUri":"https://www.google.com/maps/reviews/1"},
{"rating":4,"originalText":{"text":"Gut"},"publishTime":"2026-10-20T10:00:00Z","authorAttribution":{"displayName":"Hans","uri":"http://insecure.example/x","photoUri":"javascript:alert(1)"}},
{"rating":9,"publishTime":"2026-10-20T10:00:00Z","authorAttribution":{"displayName":"Bad rating"}},
{"rating":5,"publishTime":"2026-10-20T10:00:00Z","authorAttribution":{"displayName":""}},
{"rating":5,"publishTime":"nope","authorAttribution":{"displayName":"No date"}},
{"rating":5,"publishTime":"2026-10-19T10:00:00Z"}
]}`

func googleSettings() settings {
	return settings{GoogleKey: "G-SECRET", GooglePlace: "PLACE1", MinRating: 4, MaxItems: 6}
}

func TestFetchGoogleRequestAndMapping(t *testing.T) {
	f := newFake(t)
	f.fetch = func(string, map[string]string) (int, string, error) { return 200, googleFixture, nil }
	info, rs, err := fetchGoogle(googleSettings(), f.clock.Add(9*time.Second))
	if err != nil {
		t.Fatal(err)
	}
	req := f.requests[0]
	if req.URL != "https://places.googleapis.com/v1/places/PLACE1" ||
		req.Headers["X-Goog-Api-Key"] != "G-SECRET" ||
		req.Headers["X-Goog-FieldMask"] != "rating,userRatingCount,reviews,googleMapsUri,displayName" {
		t.Fatalf("request %+v", req)
	}
	if info.Rating != 4.8 || info.Count != 132 || info.Name != "Acme" || info.ProfileURL != "https://maps.google.com/?cid=1" {
		t.Fatalf("info %+v", info)
	}
	if len(rs) != 2 {
		t.Fatalf("reviews %+v", rs)
	}
	if rs[0].ExtID != "places/P/reviews/r1" || rs[0].Author != "Jane D." || rs[0].Body != "Great" ||
		rs[0].AuthorURL != "https://www.google.com/maps/contrib/1" || rs[0].PhotoURL != "https://lh3.example/p.jpg" ||
		rs[0].Link != "https://www.google.com/maps/reviews/1" || rs[0].Rating != 5 {
		t.Fatalf("review 0 %+v", rs[0])
	}
	// no name: hashed id; original text fallback; non-https URLs dropped.
	if len(rs[1].ExtID) != 16 || rs[1].Body != "Gut" || rs[1].AuthorURL != "" || rs[1].PhotoURL != "" {
		t.Fatalf("review 1 %+v", rs[1])
	}
}

func TestFetchGoogleToleratesMissingFields(t *testing.T) {
	f := newFake(t)
	f.fetch = func(string, map[string]string) (int, string, error) { return 200, `{}`, nil }
	info, rs, err := fetchGoogle(googleSettings(), f.clock.Add(9*time.Second))
	if err != nil || len(rs) != 0 || info.Count != 0 {
		t.Fatalf("%+v %v %v", info, rs, err)
	}
}

func TestFetchGoogleErrorsNeverCarryTheKey(t *testing.T) {
	f := newFake(t)
	dl := f.clock.Add(9 * time.Second)
	f.fetch = func(string, map[string]string) (int, string, error) {
		return 403, `{"error":{"message":"API key G-SECRET not valid"}}`, nil
	}
	_, _, err := fetchGoogle(googleSettings(), dl)
	if err == nil || strings.Contains(err.Error(), "G-SECRET") || !strings.Contains(err.Error(), "403") {
		t.Fatalf("403 error %v", err)
	}
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		return 0, "", &testErr{"get " + u + " G-SECRET failed"}
	}
	_, _, err = fetchGoogle(googleSettings(), dl)
	if err == nil || strings.Contains(err.Error(), "G-SECRET") {
		t.Fatalf("host error %v", err)
	}
}

type testErr struct{ s string }

func (e *testErr) Error() string { return e.s }

func TestFetchTruncatedAndBudget(t *testing.T) {
	f := newFake(t)
	callHost = func(fn string, args, out any) error {
		if fn == "http.fetch" {
			return roundTrip(map[string]any{"status": 200, "body": "{", "truncated": true}, out)
		}
		return f.call(fn, args, out)
	}
	if _, _, err := fetchGoogle(googleSettings(), f.clock.Add(9*time.Second)); err == nil {
		t.Fatal("truncated answer must be an error")
	}
	if _, _, err := fetchGoogle(googleSettings(), f.clock.Add(500*time.Millisecond)); err != errBudget {
		t.Fatalf("no time left: %v", err)
	}
}

func TestInvalidJSONIsAnError(t *testing.T) {
	f := newFake(t)
	f.fetch = func(string, map[string]string) (int, string, error) { return 200, `<html>`, nil }
	if _, _, err := fetchGoogle(googleSettings(), f.clock.Add(9*time.Second)); err == nil {
		t.Fatal("want error")
	}
}
