package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// fakeHost serves settings.get, entities.list and entities.get from a set of
// raw entity JSON documents keyed by id; getErr/listErr make a call fail.
type fakeHost struct {
	entities        []string
	getErr, listErr error
	getCalls        []map[string]any
}

func (h *fakeHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(`{}`), out)
		case "entities.list":
			if h.listErr != nil {
				return h.listErr
			}
			var items []string
			for _, e := range h.entities {
				var d entityDTO
				_ = json.Unmarshal([]byte(e), &d)
				if d.Status == "published" && d.SchemaSlug == args.(map[string]any)["schema_slug"] {
					items = append(items, e)
				}
			}
			return json.Unmarshal([]byte(`{"items":[`+strings.Join(items, ",")+`]}`), out)
		case "entities.get":
			a := args.(map[string]any)
			h.getCalls = append(h.getCalls, a)
			if h.getErr != nil {
				return h.getErr
			}
			for _, e := range h.entities {
				var d entityDTO
				_ = json.Unmarshal([]byte(e), &d)
				if d.Slug == a["slug"] || (a["id"] != nil && d.ID == a["id"]) {
					return json.Unmarshal([]byte(e), out)
				}
			}
			return errors.New("not found")
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }
	t.Cleanup(func() { now = time.Now })
}

const (
	evFuture = `{"id":"f1","schema_slug":"event","slug":"future","name":"Future, show","status":"published","url":"https://example.com/e/future","attributes":{"start_at":"2026-07-01T19:00","end_at":"2026-07-01T21:00","venue_name":"Hall"},"updated_at":"2026-05-01T00:00:00Z"}`
	evPast   = `{"id":"p1","schema_slug":"event","slug":"old","name":"Old","status":"published","attributes":{"start_at":"2026-01-01T19:00"}}`
	evDraft  = `{"id":"d1","schema_slug":"event","slug":"draft","name":"Draft","status":"draft","attributes":{"start_at":"2026-08-01"}}`
	evOther  = `{"id":"o1","schema_slug":"post","slug":"post","name":"Post","status":"published","attributes":{"start_at":"2026-08-01"}}`
)

func call(t *testing.T, method, pattern, file string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: map[string]string{"file": file}})
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

func TestFeedRoute(t *testing.T) {
	(&fakeHost{entities: []string{evFuture, evPast, evDraft, evOther}}).install(t)
	r := call(t, "GET", "/events.ics", "")
	if r.Status != 200 || r.Headers["Content-Type"] != "text/calendar; charset=utf-8" || r.Headers["Cache-Control"] != "public, max-age=900" {
		t.Fatalf("status/headers = %d %v", r.Status, r.Headers)
	}
	if _, ok := r.Headers["Content-Disposition"]; ok {
		t.Error("feed has a Content-Disposition")
	}
	if !strings.Contains(r.Body, "SUMMARY:Future\\, show\r\n") || !strings.Contains(r.Body, "DTSTART:20260701T190000Z\r\n") {
		t.Errorf("body = %q", r.Body)
	}
	for _, no := range []string{"Old", "Draft", "Post"} {
		if strings.Contains(r.Body, "SUMMARY:"+no) {
			t.Errorf("feed contains %s", no)
		}
	}
}

func TestFeedHostFailureIsPlain503(t *testing.T) {
	(&fakeHost{listErr: errors.New("secret internal detail")}).install(t)
	r := call(t, "GET", "/events.ics", "")
	if r.Status != 503 || r.Headers["Content-Type"] != "text/plain; charset=utf-8" || strings.Contains(r.Body, "secret") {
		t.Fatalf("response = %+v", r)
	}
}

func TestSingleEventRoute(t *testing.T) {
	h := &fakeHost{entities: []string{evFuture, evPast, evDraft, evOther}}
	h.install(t)
	r := call(t, "GET", "/event/{file}", "old.ics")
	if r.Status != 200 || r.Headers["Content-Disposition"] != `inline; filename="old.ics"` ||
		r.Headers["Content-Type"] != "text/calendar; charset=utf-8" || r.Headers["Cache-Control"] != "public, max-age=900" {
		t.Fatalf("status/headers = %d %v", r.Status, r.Headers)
	}
	if !strings.Contains(r.Body, "SUMMARY:Old\r\n") || strings.Count(r.Body, "BEGIN:VEVENT") != 1 {
		t.Errorf("body = %q", r.Body)
	}
	g := h.getCalls[0]
	if g["slug"] != "old" || g["schema_slug"] != "event" || g["parent_id"] != nil {
		t.Errorf("get args = %v", g)
	}
}

func TestSingleEventRoute404s(t *testing.T) {
	(&fakeHost{entities: []string{evFuture, evDraft, evOther}}).install(t)
	for _, file := range []string{"draft.ics", "post.ics", "missing.ics", "future", "future.txt", ".ics", "a/b.ics", "..%2f.ics", "../x.ics", "Future.ics"} {
		r := call(t, "GET", "/event/{file}", file)
		if r.Status != 404 || r.Headers["Content-Type"] != "text/plain; charset=utf-8" || r.Body != "not found" {
			t.Errorf("%q: response = %+v", file, r)
		}
	}
	if r := call(t, "POST", "/events.ics", ""); r.Status != 404 {
		t.Errorf("POST status = %d", r.Status)
	}
}

func TestSingleEventFallsBackToListScan(t *testing.T) {
	(&fakeHost{entities: []string{evFuture}, getErr: errors.New("nope")}).install(t)
	if r := call(t, "GET", "/event/{file}", "future.ics"); r.Status != 200 {
		t.Fatalf("status = %d", r.Status)
	}
}

func TestSingleEventHostDown(t *testing.T) {
	(&fakeHost{getErr: errors.New("x"), listErr: errors.New("y")}).install(t)
	r := call(t, "GET", "/event/{file}", "future.ics")
	if r.Status != 503 || r.Body != "unavailable" {
		t.Fatalf("response = %+v", r)
	}
}
