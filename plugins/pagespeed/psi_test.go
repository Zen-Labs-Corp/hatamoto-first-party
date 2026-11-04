package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestPSIRequestAndScore(t *testing.T) {
	var reqs []map[string]any
	fakeFetch(t, &reqs, func(map[string]any) (fetched, error) {
		return fetched{Status: 200, Body: `{"lighthouseResult":{"categories":{"performance":{"score":0.915}}}}`}, nil
	})
	l, err := runPSI(testKey, "https://s.example/x", "mobile")
	if err != nil || l.Status != labOK || l.Score != 92 {
		t.Fatalf("got %+v, %v", l, err)
	}
	u, _ := url.Parse(reqs[0]["url"].(string))
	q := u.Query()
	if q.Get("category") != "performance" || q.Get("fields") == "" || q.Get("strategy") != "mobile" || q.Get("url") != "https://s.example/x" {
		t.Fatalf("query = %v", q)
	}
}

func TestPSITruncatedAndTimeout(t *testing.T) {
	fakeFetch(t, nil, func(map[string]any) (fetched, error) {
		return fetched{Status: 200, Body: `{"light`, Truncated: true}, nil
	})
	l, err := runPSI(testKey, "https://s.example/x", "mobile")
	if l.Status != labError || err == nil {
		t.Fatalf("truncated: %+v, %v", l, err)
	}
	fakeFetch(t, nil, func(map[string]any) (fetched, error) {
		return fetched{}, &sdk.HostError{Code: sdk.ErrTimeout, Message: "call timed out " + testKey}
	})
	l, err = runPSI(testKey, "https://s.example/x", "mobile")
	if l.Status != labTimeout || err != nil {
		t.Fatalf("timeout: %+v, %v", l, err)
	}
}

func TestPSIErrorsHideTheKey(t *testing.T) {
	fakeFetch(t, nil, func(map[string]any) (fetched, error) { return fetched{}, errors.New("GET ...key=" + testKey) })
	_, err := runPSI(testKey, "https://s.example/x", "mobile")
	if err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
	fakeFetch(t, nil, func(map[string]any) (fetched, error) { return fetched{Status: 500}, nil })
	if _, err := runPSI(testKey, "https://s.example/x", "mobile"); err == nil || strings.Contains(err.Error(), testKey) {
		t.Fatalf("err = %v", err)
	}
}

func TestKeyTravelsInHeaderNotInURL(t *testing.T) {
	var reqs []map[string]any
	fakeFetch(t, &reqs, func(map[string]any) (fetched, error) {
		return fetched{Status: 200, Body: `{"lighthouseResult":{"categories":{"performance":{"score":0.9}}}}`}, nil
	})
	if _, err := runPSI(testKey, "https://s.example/x", "mobile"); err != nil {
		t.Fatal(err)
	}
	if _, err := queryCrux(testKey, "https://s.example/x", "PHONE", false); err != nil {
		t.Fatal(err)
	}
	if len(reqs) != 2 {
		t.Fatalf("%d requests", len(reqs))
	}
	for _, r := range reqs {
		u := r["url"].(string)
		if strings.Contains(u, testKey) || strings.Contains(u, url.QueryEscape(testKey)) || strings.Contains(u, "key=") {
			t.Fatalf("key in URL %s", u)
		}
		if r["headers"].(map[string]string)["X-Goog-Api-Key"] != testKey {
			t.Fatalf("headers = %v", r["headers"])
		}
	}
}

func TestOnlyTheTimeoutCodeIsATimeout(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want bool
	}{
		{&sdk.HostError{Code: sdk.ErrTimeout, Message: "x"}, true},
		{errBudget, true},
		{&sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "deadline exceeded, timeout, timed out"}, false},
		{errors.New("context deadline exceeded"), false},
	} {
		if got := isTimeout(tc.err); got != tc.want {
			t.Errorf("isTimeout(%v) = %v", tc.err, got)
		}
	}
	fakeFetch(t, nil, func(map[string]any) (fetched, error) {
		return fetched{}, &sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "upstream said: deadline exceeded"}
	})
	if l, err := runPSI(testKey, "https://s.example/x", "mobile"); l.Status != labError || err == nil {
		t.Fatalf("got %+v, %v", l, err)
	}
}
