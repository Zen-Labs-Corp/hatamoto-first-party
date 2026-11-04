package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func call(t *testing.T, method, pattern string) (int, map[string]json.RawMessage) {
	t.Helper()
	res, err := handleRoute(sdk.HTTPRequest{Method: method, Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Body), &body); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return res.Status, body
}

func TestOverviewWithoutFeeds(t *testing.T) {
	newFake(t)
	status, body := call(t, "GET", "/overview")
	if status != http.StatusOK || string(body["feeds"]) != "[]" || string(body["latest"]) != "[]" {
		t.Fatalf("%d %s", status, body)
	}
}

func TestOverviewMixedStates(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha", "beta", "gamma")
	cfg, _ := loadFeeds()
	f.feeds[cfg[0].Key] = feedRow{Key: cfg[0].Key, Name: "alpha", Status: "ok", ItemCount: 3, LastAttempt: f.clock.Add(-12 * time.Minute)}
	f.feeds[cfg[1].Key] = feedRow{Key: cfg[1].Key, Name: "beta", Status: "HTTP 404"}
	addItem(f, cfg[0].Key, "g1", "Title", time.Date(2026, 11, 3, 8, 0, 0, 0, time.UTC))
	_, body := call(t, "GET", "/overview")
	var feeds []overviewFeed
	if err := json.Unmarshal(body["feeds"], &feeds); err != nil || len(feeds) != 3 {
		t.Fatalf("%v %s", err, body["feeds"])
	}
	if feeds[0].Status != "ok" || feeds[0].Items != 3 || feeds[0].LastFetch != "2026-11-04T11:48:00Z" {
		t.Fatalf("%+v", feeds[0])
	}
	if feeds[1].Status != "HTTP 404" || feeds[1].LastFetch != "" || feeds[2].Status != "pending" {
		t.Fatalf("%+v %+v", feeds[1], feeds[2])
	}
	var latest []overviewItem
	_ = json.Unmarshal(body["latest"], &latest)
	if len(latest) != 1 || latest[0].Title != "Title" || latest[0].Source != "alpha" || latest[0].Published != "2026-11-03T08:00:00Z" {
		t.Fatalf("%+v", latest)
	}
}

func TestOverviewLatestCappedAtTwenty(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	cfg, _ := loadFeeds()
	f.feeds[cfg[0].Key] = feedRow{Key: cfg[0].Key, Name: "alpha"}
	for i := 0; i < 30; i++ {
		addItem(f, cfg[0].Key, fmt.Sprintf("g%02d", i), "t", f.clock.Add(-time.Duration(i)*time.Hour))
	}
	_, body := call(t, "GET", "/overview")
	var latest []overviewItem
	_ = json.Unmarshal(body["latest"], &latest)
	if len(latest) != 20 {
		t.Fatalf("latest %d", len(latest))
	}
}

func TestOverviewFailsWithoutDetails(t *testing.T) {
	f := newFake(t)
	f.dbDown = true
	setFeeds(f, "alpha")
	res, _ := handleRoute(sdk.HTTPRequest{Method: "GET", Pattern: "/overview"})
	if res.Status != http.StatusInternalServerError || res.Body != `{"error":"internal error"}` || strings.Contains(f.logged(), "unavailable") {
		t.Fatalf("%d %s %s", res.Status, res.Body, f.logged())
	}
}

func TestFetchNowIgnoresDueTime(t *testing.T) {
	f := newFake(t)
	setFeeds(f, "alpha")
	serve(f, fetchRes{Status: 200, Body: rss(2)})
	status, body := call(t, "POST", "/fetch-now")
	if status != http.StatusOK || !strings.Contains(string(body["results"]), `"alpha"`) || !strings.Contains(string(body["results"]), `"ok"`) {
		t.Fatalf("%d %s", status, body)
	}
	f.clock = f.clock.Add(time.Minute) // not due by the clock
	call(t, "POST", "/fetch-now")
	if len(f.requests) != 2 {
		t.Fatalf("requests %d", len(f.requests))
	}
}

func TestFetchNowWithoutFeedsGivesEmptyList(t *testing.T) {
	newFake(t)
	_, body := call(t, "POST", "/fetch-now")
	if string(body["results"]) != "[]" {
		t.Fatalf("%s", body["results"])
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	newFake(t)
	res, _ := handleRoute(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"})
	if res.Status != http.StatusNotFound {
		t.Fatal(res.Status)
	}
}

func TestRoutesRegisteredWithGuest(t *testing.T) {
	newFake(t)
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"})
	if _, err := guest.Handle(guest.HTTPExport, in); err != nil {
		t.Fatal(err)
	}
}
