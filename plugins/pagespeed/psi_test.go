package main

import (
	"errors"
	"net/url"
	"strings"
	"testing"
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
		return fetched{}, errors.New("context deadline exceeded " + testKey)
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
