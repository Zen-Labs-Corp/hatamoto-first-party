package main

import (
	"encoding/json"
	"errors"
	"testing"
)

// refHost answers entities.get from raw documents keyed by id and counts calls.
func refHost(t *testing.T, docs map[string]string, getErr error) *int {
	t.Helper()
	calls := 0
	callHost = func(fn string, args, out any) error {
		if fn != "entities.get" {
			t.Fatalf("unexpected host call %q", fn)
		}
		calls++
		if getErr != nil {
			return getErr
		}
		doc, ok := docs[args.(map[string]any)["id"].(string)]
		if !ok {
			return errors.New("not found")
		}
		return json.Unmarshal([]byte(doc), out)
	}
	return &calls
}

const refVenueDoc = `{"id":"v1","schema_slug":"calendar-venue","name":"Blue Room","status":"published","url":"https://example.org/v/blue",
	"attributes":{"address":"12 High St","city":"Bonn","postcode":"53111","latitude":50.73,"longitude":"7.1","website":"https://example.org"}}`

func TestRefVenueResolves(t *testing.T) {
	refHost(t, map[string]string{"v1": refVenueDoc}, nil)
	v, ok := loadVenue("v1")
	if !ok || v.Name != "Blue Room" || v.URL != "https://example.org/v/blue" || v.Address != "12 High St" ||
		v.Lat == nil || *v.Lat != 50.73 || v.Lon == nil || *v.Lon != 7.1 {
		t.Fatalf("venue = %+v, %v", v, ok)
	}
	if got := v.line(); got != "Blue Room, 12 High St, Bonn" {
		t.Errorf("line = %q", got)
	}
	if got := (Venue{Name: "Hall", City: "Bonn"}).line(); got != "Hall, Bonn" {
		t.Errorf("line = %q", got)
	}
	name, addr, city, ref := venueOrText(Event{VenueID: "v1", Venue: "Text", Address: "Text St"})
	if name != "Blue Room" || addr != "12 High St" || city != "Bonn" || ref == nil {
		t.Errorf("venueOrText = %q %q %q %v", name, addr, city, ref)
	}
}

func TestRefOrganizerAndSeries(t *testing.T) {
	refHost(t, map[string]string{
		"o1": `{"name":"Jazz Club","status":"published","attributes":{"email":"hi@example.org","website":"https://example.org"}}`,
		"s1": `{"name":"Summer","status":"published","url":"https://example.org/s"}`,
	}, nil)
	o, ok := loadOrganizer("o1")
	if !ok || o.Name != "Jazz Club" || o.Email != "hi@example.org" || o.Website != "https://example.org" {
		t.Fatalf("organizer = %+v, %v", o, ok)
	}
	s, ok := loadSeries("s1")
	if !ok || s.Name != "Summer" || s.URL != "https://example.org/s" {
		t.Fatalf("series = %+v, %v", s, ok)
	}
}

func TestRefUnpublishedOrErrorFallsBackToText(t *testing.T) {
	refHost(t, map[string]string{"v1": `{"name":"Hidden","status":"draft","attributes":{}}`}, nil)
	if _, ok := loadVenue("v1"); ok {
		t.Error("draft venue resolved")
	}
	if _, ok := loadOrganizer("v1"); ok {
		t.Error("draft organizer resolved")
	}
	e := Event{VenueID: "v1", Venue: "Text Hall", Address: "1 Main St", VenueCity: "Bonn"}
	name, addr, city, ref := venueOrText(e)
	if name != "Text Hall" || addr != "1 Main St" || city != "Bonn" || ref != nil {
		t.Errorf("venueOrText = %q %q %q %v", name, addr, city, ref)
	}
	refHost(t, nil, errors.New("host down"))
	if _, ok := loadSeries("s1"); ok {
		t.Error("series resolved on host error")
	}
	if name, _, _, ref := venueOrText(e); name != "Text Hall" || ref != nil {
		t.Errorf("after host error: %q %v", name, ref)
	}
}

func TestRefEmptyIDMakesNoHostCall(t *testing.T) {
	calls := refHost(t, nil, nil)
	if _, ok := loadVenue(""); ok {
		t.Error("empty venue id resolved")
	}
	loadOrganizer("")
	loadSeries("")
	venueOrText(Event{Venue: "Text"})
	if *calls != 0 {
		t.Errorf("host calls = %d", *calls)
	}
}
