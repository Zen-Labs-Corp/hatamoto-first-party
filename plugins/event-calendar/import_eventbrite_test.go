package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const (
	ebToken = "SECRET-TOKEN-123"
	ebOrg   = "123456789"
	ebBase  = "https://www.eventbriteapi.com/v3/organizations/" + ebOrg + "/events/?status=live&time_filter=current_future&expand=venue&page_size=50"
)

// Two pages as the v3 organization events endpoint answers them (the fields
// the import reads, plus some it ignores).
const ebPage1 = `{"pagination":{"object_count":3,"page_number":1,"page_size":50,"page_count":2,"has_more_items":true,"continuation":"dGVzdA"},
"events":[
{"id":"1001","name":{"text":"Jazz night","html":"<p>Jazz night</p>"},"description":{"text":"Live jazz.\nBring friends."},"url":"https://www.eventbrite.com/e/jazz-night-tickets-1001",
 "start":{"timezone":"Europe/Berlin","local":"2027-06-12T20:00:00","utc":"2027-06-12T18:00:00Z"},"end":{"timezone":"Europe/Berlin","local":"2027-06-12T23:00:00","utc":"2027-06-12T21:00:00Z"},
 "status":"live","online_event":false,"is_free":false,"venue_id":"55",
 "venue":{"name":"The Cellar","address":{"address_1":"Main Street 1","city":"Example City","country":"DE"}}},
{"id":"1002","name":{"text":"Online talk"},"description":{"text":""},"url":"https://www.eventbrite.com/e/online-talk-tickets-1002",
 "start":{"timezone":"UTC","utc":"2027-06-13T18:00:00Z"},"end":{"utc":"2027-06-13T19:00:00Z"},"status":"live","online_event":true,"venue":null}]}`

const ebPage2 = `{"pagination":{"object_count":3,"page_number":2,"page_size":50,"page_count":2,"has_more_items":false},
"events":[
{"id":"1003","name":{"text":"Cancelled fair"},"description":{"text":"Rained out."},"url":"https://www.eventbrite.com/e/fair-tickets-1003",
 "start":{"utc":"2027-07-01T09:00:00Z"},"end":{"utc":"2027-07-01T17:00:00Z"},"status":"canceled","venue":null}]}`

func ebRun(pages map[string]fakePage) *runHost {
	return &runHost{name: runEditDates, runID: "eb", params: map[string]any{"action": "import_source", "name": "Tickets"}, pages: pages,
		settings: `{"eventbrite_token":"` + ebToken + `","import_sources":[{"name":"Tickets","kind":"eventbrite","organization_id":"` + ebOrg + `","schedule":"daily"}]}`}
}

func TestEventbriteImportsPagesWithContinuation(t *testing.T) {
	h := ebRun(map[string]fakePage{
		ebBase:                          {status: 200, body: ebPage1},
		ebBase + "&continuation=dGVzdA": {status: 200, body: ebPage2},
	})
	h.install(t)
	steps := h.runFetch(t)
	if h.failed != "" || steps != 2 {
		t.Fatalf("failed %q after %d steps", h.failed, steps)
	}
	if len(h.fetched) != 2 || h.fetched[0] != ebBase || h.fetched[1] != ebBase+"&continuation=dGVzdA" {
		t.Fatalf("fetched %v", h.fetched)
	}
	for i, hdr := range h.headers {
		if hdr["Authorization"] != "Bearer "+ebToken || hdr["Accept"] != "application/json" {
			t.Errorf("request %d headers %v", i, hdr)
		}
	}
	recs := h.records(t)
	if len(recs) != 3 {
		t.Fatalf("%d records", len(recs))
	}
	byTitle := map[string]importRecord{}
	for _, r := range recs {
		byTitle[r.Ev.Title] = r
		if r.Kind != "eventbrite" || r.Source != "Tickets" || r.Key != importKey("eventbrite", "Tickets", r.Ev.UID) || !strings.HasPrefix(r.Key, "eventbrite-tickets-") {
			t.Errorf("record %+v", r)
		}
	}
	jazz := byTitle["Jazz night"]
	if jazz.Ev.Venue != "The Cellar" || jazz.Ev.Address != "Main Street 1" || jazz.Ev.City != "Example City" || jazz.Ev.Description != "Live jazz.\nBring friends." ||
		jazz.Ev.TicketURL != "https://www.eventbrite.com/e/jazz-night-tickets-1001" || jazz.Ev.Start.UTC().Format("2006-01-02T15:04") != "2027-06-12T18:00" || jazz.Ev.End.IsZero() || jazz.Ev.OnlineURL != "" {
		t.Errorf("jazz %+v", jazz.Ev)
	}
	if byTitle["Cancelled fair"].Ev.Status != "cancelled" || byTitle["Online talk"].Ev.Venue != "" {
		t.Errorf("records %+v", byTitle)
	}

	// a second run maps to the same keys
	again := ebRun(h.pages)
	again.install(t)
	again.runFetch(t)
	for i, r := range again.records(t) {
		if r.Key != recs[i].Key {
			t.Errorf("key %d changed: %s != %s", i, r.Key, recs[i].Key)
		}
	}
}

func TestEventbriteStopsAtTheLastPage(t *testing.T) {
	loop := strings.Replace(ebPage1, `"continuation":"dGVzdA"`, `"continuation":"same"`, 1)
	h := ebRun(map[string]fakePage{ebBase: {status: 200, body: loop}, ebBase + "&continuation=same": {status: 200, body: loop}})
	h.install(t)
	if steps := h.runFetch(t); steps != maxEventbritePages || h.failed != "" {
		t.Errorf("%d steps, failed %q, want %d pages", steps, h.failed, maxEventbritePages)
	}
}

func TestEventbriteTokenNeverLeaks(t *testing.T) {
	for name, page := range map[string]fakePage{
		"refused":   {status: 401, body: `{"error":"INVALID_AUTH","error_description":"` + ebToken + `"}`},
		"forbidden": {status: 403, body: ebToken},
		"server":    {status: 500, body: ebToken},
		"fetch":     {err: &sdk.HostError{Code: sdk.ErrBlocked, Message: ebToken}},
	} {
		t.Run(name, func(t *testing.T) {
			h := ebRun(map[string]fakePage{ebBase: page})
			h.created = true
			h.install(t)
			h.runFetch(t)
			h.phase = "transform"
			h.step(t)
			if !strings.HasPrefix(h.failed, "No calendar could be read") && h.failed == "" && h.finished == nil {
				t.Fatalf("failed %q", h.failed)
			}
			summary, _ := json.Marshal(h.finished)
			all := []string{h.failed, h.progress, string(summary)}
			for k, v := range h.buffer {
				all = append(all, k, string(v))
			}
			for _, s := range all {
				if strings.Contains(s, ebToken) {
					t.Errorf("the token shows up in %q", s)
				}
			}
		})
	}

	h := ebRun(map[string]fakePage{ebBase: {status: 401}})
	h.install(t)
	h.runFetch(t)
	if note := string(h.buffer["err/00"]); !strings.Contains(note, "token was refused") {
		t.Errorf("note %s", note)
	}

	h = ebRun(nil)
	h.settings = `{"import_sources":[{"name":"Tickets","kind":"eventbrite","organization_id":"` + ebOrg + `"}]}`
	h.install(t)
	h.runFetch(t)
	if note := string(h.buffer["err/00"]); !strings.Contains(note, "no Eventbrite token") || len(h.fetched) != 0 {
		t.Errorf("note %s, fetched %v", note, h.fetched)
	}
}
