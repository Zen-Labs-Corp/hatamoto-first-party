package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func callAdmin(t *testing.T, method string, user *sdk.HTTPUser) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: "/admin/repeating", User: user})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var r sdk.HTTPResponse
	if err := json.Unmarshal(out, &r); err != nil {
		t.Fatal(err)
	}
	return r
}

const (
	yogaEntity = `{"id":"y1","schema_slug":"calendar-event","slug":"yoga","name":"Yoga in the park","status":"published","attributes":{
"start_at":"2026-06-02T18:00","end_at":"2026-06-02T19:00","repeat":"weekly","repeat_weekdays":["tu"],
"repeat_skip_dates":["2026-06-09"],"repeat_overrides":["2026-06-16|2026-06-17T19:00"],"source_uid":"dup-r1"}}`
	bookEntity = `{"id":"b1","schema_slug":"calendar-event","slug":"book-club","name":"Book club","status":"published","attributes":{
"start_at":"2026-06-11T18:30","repeat":"monthly","repeat_by":"nth_weekday","repeat_nth":2,"repeat_count":12}}`
)

func TestRepeatingList(t *testing.T) {
	h := &fakeHost{entities: []string{yogaEntity, bookEntity},
		dbRows: `[{"id":"y1","slug":"yoga","name":"Yoga in the park","source_uid":"dup-r1"},{"id":"b1","slug":"book-club","name":"Book club","source_uid":""},{"id":"gone","slug":"gone","name":"Gone"}]`}
	h.install(t)

	r := callAdmin(t, "GET", &sdk.HTTPUser{ID: "u", Role: "editor"})
	if r.Status != 200 || !strings.HasPrefix(r.Headers["Content-Type"], "application/json") {
		t.Fatalf("status %d headers %v", r.Status, r.Headers)
	}
	var got adminList
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil {
		t.Fatal(err)
	}
	if !got.Ready || got.Role != "editor" || len(got.Events) != 3 {
		t.Fatalf("list = %+v", got)
	}
	yoga, book, gone := got.Events[0], got.Events[1], got.Events[2]
	if yoga.Rule != "Weekly, Tue, ends never" || !yoga.Owned || yoga.Slug != "yoga" {
		t.Errorf("yoga = %+v", yoga)
	}
	if book.Rule != "Monthly, 2nd Thu, 12x" || book.Owned {
		t.Errorf("book = %+v", book)
	}
	if len(gone.Dates) != 0 || gone.Dates == nil {
		t.Errorf("an event that cannot be read keeps an empty date list: %+v", gone)
	}
	if len(yoga.Dates) != adminDates {
		t.Fatalf("yoga dates = %d", len(yoga.Dates))
	}
	want := []adminDate{
		{Day: "2026-06-02", Date: "2026-06-02", Start: "18:00", End: "19:00", State: "normal"},
		{Day: "2026-06-09", Date: "2026-06-09", Start: "18:00", End: "19:00", State: "skipped"},
		{Day: "2026-06-16", Date: "2026-06-17", Start: "19:00", End: "20:00", State: "changed"},
		{Day: "2026-06-23", Date: "2026-06-23", Start: "18:00", End: "19:00", State: "normal"},
	}
	for i, w := range want {
		if yoga.Dates[i] != w {
			t.Errorf("date %d = %+v, want %+v", i, yoga.Dates[i], w)
		}
	}
	if book.Dates[0].Date != "2026-06-11" || book.Dates[0].End != "" {
		t.Errorf("book first date = %+v", book.Dates[0])
	}
}

func TestRepeatingListEdges(t *testing.T) {
	h := &fakeHost{dbRows: `null`}
	h.install(t)
	var got adminList
	r := callAdmin(t, "GET", &sdk.HTTPUser{Role: "owner"})
	if err := json.Unmarshal([]byte(r.Body), &got); err != nil || got.Ready || got.Events == nil || len(got.Events) != 0 {
		t.Errorf("index not ready: %s (%v)", r.Body, err)
	}
	if r := callAdmin(t, "GET", nil); r.Status != 403 {
		t.Errorf("no user: status %d", r.Status)
	}
	if r := callAdmin(t, "POST", &sdk.HTTPUser{Role: "owner"}); r.Status != 404 {
		t.Errorf("POST: status %d", r.Status)
	}
	(&fakeHost{}).install(t) // no db
	if r := callAdmin(t, "GET", &sdk.HTTPUser{Role: "owner"}); r.Status != 503 || strings.Contains(r.Body, "no db") {
		t.Errorf("db failure: %d %q", r.Status, r.Body)
	}
}
