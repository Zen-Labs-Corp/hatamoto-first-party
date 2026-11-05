package main

import (
	"encoding/json"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

func overviewPages(t *testing.T, r sdk.HTTPResponse) []pageRow {
	t.Helper()
	if r.Status != 200 || r.Headers["Content-Type"] != "application/json" {
		t.Fatalf("response %d %q", r.Status, r.Headers["Content-Type"])
	}
	var d struct {
		Pages []pageRow `json:"pages"`
	}
	if err := json.Unmarshal([]byte(r.Body), &d); err != nil || d.Pages == nil {
		t.Fatalf("body %q: %v", r.Body, err)
	}
	return d.Pages
}

func TestOverviewMapsRows(t *testing.T) {
	(&fakeHost{db: rowsDB(`[["` + testID + `","/blog/a","4.6",41,30,8,2,0,1],["b","","3.0","2",1,0,1,0,0],["short"]]`)}).install(t)
	pages := overviewPages(t, handleOverview())
	if len(pages) != 2 {
		t.Fatalf("%d pages", len(pages))
	}
	want := pageRow{EntityID: testID, Path: "/blog/a", Average: "4.6", Count: 41, Dist: [5]int{30, 8, 2, 0, 1}}
	if pages[0] != want {
		t.Fatalf("page %+v", pages[0])
	}
	if pages[1].Count != 2 || pages[1].Path != "" || pages[1].Dist != [5]int{1, 0, 1, 0, 0} {
		t.Fatalf("page %+v", pages[1])
	}
}

func TestOverviewEmptyAndDatabaseError(t *testing.T) {
	for name, db := range map[string]func(hostCall, any) error{
		"no rows":  rowsDB(`[]`),
		"db error": func(hostCall, any) error { return errors.New("unavailable") },
	} {
		(&fakeHost{db: db}).install(t)
		if pages := overviewPages(t, handleOverview()); len(pages) != 0 {
			t.Errorf("%s: %+v", name, pages)
		}
	}
}

func TestServeOverviewRoute(t *testing.T) {
	(&fakeHost{db: rowsDB(`[]`)}).install(t)
	r, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/overview"})
	if err != nil || r.Status != 200 {
		t.Fatalf("%v %d", err, r.Status)
	}
}
