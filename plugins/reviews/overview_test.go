package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func call(t *testing.T, method, pattern string) (int, string, overview) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Path: pattern, Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	var o overview
	_ = json.Unmarshal([]byte(res.Body), &o)
	return res.Status, res.Body, o
}

func TestOverviewWithoutKeysIsEmptyAndOK(t *testing.T) {
	newFake(t)
	status, body, o := call(t, "GET", "/overview")
	if status != 200 || len(o.Sources) != 2 || o.Sources[0].State != "not_set_up" || o.Sources[1].State != "not_set_up" ||
		o.Sources[0].Missing != "API key and place ID" || o.Sources[1].Missing != "API key and business unit ID" ||
		len(o.Reviews) != 0 || o.GoogleLimit != 5 || o.MinRating != 4 || o.MaxItems != 6 || o.Error != "" {
		t.Fatalf("%d %s", status, body)
	}
	if !strings.Contains(body, `"reviews":[]`) {
		t.Fatalf("reviews must be an array: %s", body)
	}
}

func TestOverviewStatesAndReviews(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	_ = refreshAll(true)
	good := f.fetch
	f.fetch = func(u string, h map[string]string) (int, string, error) {
		if strings.Contains(u, "trustpilot") {
			return 401, `{"message":"invalid key"}`, nil
		}
		return good(u, h)
	}
	f.clock = f.clock.Add(2 * time.Hour)
	status, _, o := call(t, "POST", "/refresh-now")
	if status != 200 {
		t.Fatal(status)
	}
	g, tp := o.Sources[0], o.Sources[1]
	if g.State != "connected" || g.Rating != 4.8 || g.Count != 132 || g.RefreshedAt == "" {
		t.Fatalf("google %+v", g)
	}
	if tp.State != "error" || !strings.Contains(tp.LastError, "401") {
		t.Fatalf("trustpilot %+v", tp)
	}
	if len(o.Reviews) == 0 || o.Reviews[0].Author != "Jane D." || o.Reviews[0].Date != "2026-10-30" || o.Reviews[0].SourceLabel != "Google" {
		t.Fatalf("reviews %+v", o.Reviews)
	}
}

func TestOverviewNeverLeaksASecret(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	f.set("google_api_key", "SECRET123")
	f.fetch = func(u string, _ map[string]string) (int, string, error) {
		if strings.Contains(u, "trustpilot") {
			return 403, `{"message":"bad key T-SECRET"}`, nil
		}
		return 403, `{"error":{"message":"bad key SECRET123"}}`, nil
	}
	_, body, _ := call(t, "POST", "/refresh-now")
	_, body2, _ := call(t, "GET", "/overview")
	for _, b := range []string{body, body2, f.logged()} {
		if strings.Contains(b, "SECRET123") || strings.Contains(b, "T-SECRET") {
			t.Fatalf("secret leaked: %s", b)
		}
	}
}

func TestRefreshNowForcesFetch(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	bothAPIs(f)
	_ = refreshAll(false)
	f.requests = nil
	call(t, "POST", "/refresh-now")
	if g, tp := hosts(f); g != 1 || tp != 2 {
		t.Fatalf("g=%d tp=%d", g, tp)
	}
}

func TestOverviewDbDownIsStill200(t *testing.T) {
	f := newFake(t)
	f.setBoth()
	f.dbDown = true
	status, _, o := call(t, "GET", "/overview")
	if status != 200 || o.Error == "" || len(o.Sources) != 0 {
		t.Fatalf("%d %+v", status, o)
	}
	if status, _, _ = call(t, "POST", "/refresh-now"); status != 200 {
		t.Fatal(status)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	newFake(t)
	if status, _, _ := call(t, "GET", "/nope"); status != 404 {
		t.Fatal(status)
	}
}

func TestSelectReviews(t *testing.T) {
	all := []review{rv("google", "a", 5, 1), rv("google", "b", 3, 9), rv("trustpilot", "c", 4, 5), rv("google", "d", 5, 7)}
	got := selectReviews(all, 4, 0)
	if len(got) != 3 || got[0].ExtID != "d" || got[1].ExtID != "c" || got[2].ExtID != "a" {
		t.Fatalf("%+v", got)
	}
	if got := selectReviews(all, 4, 2); len(got) != 2 {
		t.Fatalf("limit: %+v", got)
	}
	var many []review
	for i := 0; i < 60; i++ {
		many = append(many, rv("google", "x"+string(rune('a'+i%26))+string(rune('a'+i/26)), 5, 1+i%28))
	}
	if got := selectReviews(many, 1, 100); len(got) != 48 {
		t.Fatalf("cap: %d", len(got))
	}
	if got := selectReviews(nil, 1, 5); got == nil || len(got) != 0 {
		t.Fatal("empty must be a non-nil slice")
	}
}
