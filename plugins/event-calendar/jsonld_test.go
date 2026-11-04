package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func enrich(t *testing.T, d sdk.JSONLDDoc) sdk.JSONLDDoc {
	t.Helper()
	in, _ := json.Marshal(d)
	out, err := guest.Handle(sdk.JSONLDEnrich.Export(), in)
	if err != nil {
		t.Fatalf("filter returned an error: %v", err)
	}
	var got sdk.JSONLDDoc
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	return got
}

const evJazz = `{"id":"3f2a","schema_slug":"event","slug":"jazz","name":"Winter jazz night","status":"published",
 "attributes":{"start_at":"2027-02-14T19:00:00Z","end_at":"2027-02-14T21:30:00Z","venue_name":"The Blue Room","venue_address":"12 High St",
 "ticket_url":"https://tickets.example.com/jazz","image":"/media/jazz.jpg"}}`

func baseDoc() sdk.JSONLDDoc {
	return sdk.JSONLDDoc{
		Entity:  sdk.EntityRef{ID: "3f2a", SchemaSlug: "event"},
		BaseURL: "https://example.com",
		Doc: map[string]any{
			"@context": "https://schema.org", "@type": "Thing", "name": "Winter jazz night",
			"url": "https://example.com/events/winter-jazz-night",
		},
	}
}

func TestEnrichGolden(t *testing.T) {
	(&fakeHost{entities: []string{evJazz}}).install(t)
	got := enrich(t, baseDoc())
	want := map[string]any{
		"@context": "https://schema.org", "@type": "Event", "name": "Winter jazz night",
		"url":       "https://example.com/events/winter-jazz-night",
		"startDate": "2027-02-14T19:00:00Z", "endDate": "2027-02-14T21:30:00Z",
		"eventStatus":         "https://schema.org/EventScheduled",
		"eventAttendanceMode": "https://schema.org/OfflineEventAttendanceMode",
		"location":            map[string]any{"@type": "Place", "name": "The Blue Room", "address": "12 High St"},
		"offers":              map[string]any{"@type": "Offer", "url": "https://tickets.example.com/jazz"},
		"image":               "https://example.com/media/jazz.jpg",
	}
	if !reflect.DeepEqual(got.Doc, want) {
		t.Fatalf("doc = %v\nwant %v", got.Doc, want)
	}
	if got.Entity.ID != "3f2a" || got.BaseURL != "https://example.com" {
		t.Errorf("entity/base_url changed: %+v", got)
	}
}

func TestEnrichKeepsExistingKeys(t *testing.T) {
	(&fakeHost{entities: []string{evJazz}}).install(t)
	d := baseDoc()
	loc := map[string]any{"@type": "Place", "name": "Mine"}
	d.Doc["startDate"] = "2030-01-01"
	d.Doc["location"] = loc
	d.Doc["image"] = "https://cdn.example.com/x.jpg"
	got := enrich(t, d)
	if got.Doc["startDate"] != "2030-01-01" || got.Doc["image"] != "https://cdn.example.com/x.jpg" || !reflect.DeepEqual(got.Doc["location"], loc) {
		t.Fatalf("doc = %v", got.Doc)
	}
	if got.Doc["endDate"] != "2027-02-14T21:30:00Z" || got.Doc["@type"] != "Event" {
		t.Errorf("missing keys not added: %v", got.Doc)
	}
	if d.Doc["@type"] != "Thing" {
		t.Error("input doc mutated")
	}
}

func TestEnrichAllDayAndZone(t *testing.T) {
	h := &fakeHost{entities: []string{`{"id":"a","schema_slug":"event","slug":"m","name":"M","status":"published",
	 "attributes":{"start_at":"2027-03-07","end_at":"2027-03-08","all_day":true}}`}}
	h.install(t)
	d := baseDoc()
	d.Entity.ID = "a"
	got := enrich(t, d)
	if got.Doc["startDate"] != "2027-03-07" || got.Doc["endDate"] != "2027-03-08" {
		t.Fatalf("doc = %v", got.Doc)
	}
	if _, ok := got.Doc["location"]; ok {
		t.Error("location without a venue")
	}
	if _, ok := got.Doc["offers"]; ok {
		t.Error("offers without a ticket url")
	}
}

func TestEnrichLeavesOtherThingsAlone(t *testing.T) {
	h := &fakeHost{entities: []string{evJazz}}
	h.install(t)
	d := baseDoc()
	d.Entity.SchemaSlug = "post"
	if got := enrich(t, d); !reflect.DeepEqual(got.Doc, d.Doc) || len(h.getCalls) != 0 {
		t.Fatalf("doc = %v, get calls %d", got.Doc, len(h.getCalls))
	}
}

func TestEnrichFailsOpen(t *testing.T) {
	d := baseDoc()
	(&fakeHost{getErr: errors.New("boom")}).install(t)
	if got := enrich(t, d); !reflect.DeepEqual(got.Doc, d.Doc) {
		t.Errorf("host error: doc = %v", got.Doc)
	}
	(&fakeHost{entities: []string{`{"id":"3f2a","schema_slug":"event","slug":"j","name":"J","status":"draft","attributes":{"start_at":"2027-01-01"}}`}}).install(t)
	if got := enrich(t, d); !reflect.DeepEqual(got.Doc, d.Doc) {
		t.Errorf("draft: doc = %v", got.Doc)
	}
	(&fakeHost{entities: []string{`{"id":"3f2a","schema_slug":"event","slug":"j","name":"J","status":"published","attributes":{"start_at":"soon"}}`}}).install(t)
	if got := enrich(t, d); !reflect.DeepEqual(got.Doc, d.Doc) {
		t.Errorf("bad start: doc = %v", got.Doc)
	}
}

func TestEnrichNilDoc(t *testing.T) {
	(&fakeHost{entities: []string{evJazz}}).install(t)
	d := baseDoc()
	d.Doc = nil
	got := enrich(t, d)
	if got.Doc["@type"] != "Event" || got.Doc["startDate"] != "2027-02-14T19:00:00Z" {
		t.Fatalf("doc = %v", got.Doc)
	}
}
