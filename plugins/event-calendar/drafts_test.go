package main

import (
	"errors"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func TestScanDraftsReadsPagesByCursor(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(450)}
	h.install(t)
	rows, capped, err := scanDrafts(defaultSettings(), "", clock().Add(time.Hour))
	if err != nil || capped || len(rows) != 450 {
		t.Fatalf("rows %d capped %v err %v", len(rows), capped, err)
	}
	// the default accepts two types: the second one has no drafts
	if len(h.listed) != 4 || h.listed[0]["status"] != "draft" || h.listed[0]["limit"] != 200 || h.listed[1]["cursor"] != "c200" {
		t.Errorf("list calls = %v", h.listed)
	}
	if _, has := h.listed[0]["search"]; has {
		t.Errorf("an empty search was sent: %v", h.listed[0])
	}
	if rows[0].Name != "Draft 0449" || !rows[0].HasStart || rows[0].Start.Month() != time.June {
		t.Errorf("first row = %+v", rows[0])
	}
}

func TestScanDraftsStopsAtTheCap(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(650)}
	h.install(t)
	rows, capped, err := scanDrafts(defaultSettings(), "", clock().Add(time.Hour))
	if err != nil || !capped || len(rows) != maxDraftScan {
		t.Fatalf("rows %d capped %v err %v", len(rows), capped, err)
	}
	if len(h.listed) != 3 {
		t.Errorf("read %d pages, want 3", len(h.listed))
	}
	// exactly the cap and nothing more is not capped
	h = &eventsHost{drafts: seedDrafts(500)}
	h.install(t)
	if rows, capped, _ := scanDrafts(defaultSettings(), "", clock().Add(time.Hour)); capped || len(rows) != 500 {
		t.Errorf("500 drafts: %d rows, capped %v", len(rows), capped)
	}
}

func TestScanDraftsStopsAtTheBudget(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(450)}
	h.install(t)
	at := time.Date(2027, 3, 1, 12, 0, 0, 0, time.UTC)
	clock = func() time.Time { return at }
	t.Cleanup(func() { clock = time.Now })
	h.onList = func() { at = at.Add(draftBudget) } // each page read burns the budget
	rows, capped, err := scanDrafts(defaultSettings(), "", at.Add(draftBudget))
	if err != nil || !capped || len(rows) != 200 || len(h.listed) != 1 {
		t.Errorf("rows %d capped %v err %v calls %d", len(rows), capped, err, len(h.listed))
	}
}

func TestScanDraftsSkipsAMissingType(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(3), listErr: map[string]error{legacySchemaSlug: &sdk.HostError{Code: sdk.ErrNotFound}}}
	h.install(t)
	if rows, _, err := scanDrafts(defaultSettings(), "", clock().Add(time.Hour)); err != nil || len(rows) != 3 {
		t.Errorf("rows %d err %v", len(rows), err)
	}
	h = &eventsHost{drafts: seedDrafts(3), listErr: map[string]error{defaultNewSlug: &sdk.HostError{Code: sdk.ErrNotFound}}}
	h.install(t)
	if rows, _, err := scanDrafts(defaultSettings(), "", clock().Add(time.Hour)); err != nil || len(rows) != 0 {
		t.Errorf("a missing first type: rows %d err %v", len(rows), err)
	}
	h = &eventsHost{drafts: seedDrafts(3), listErr: map[string]error{defaultNewSlug: errors.New("boom")}}
	h.install(t)
	if _, _, err := scanDrafts(defaultSettings(), "", clock().Add(time.Hour)); err == nil {
		t.Error("an error on the first type is lost")
	}
}

func TestScanDraftsSearchIsAlsoAppliedHere(t *testing.T) {
	h := &eventsHost{drafts: seedDrafts(30)}
	h.install(t)
	rows, _, _ := scanDrafts(defaultSettings(), "draft 002", clock().Add(time.Hour))
	if len(rows) != 10 || h.listed[0]["search"] != "draft 002" {
		t.Errorf("rows %d, search arg %v", len(rows), h.listed[0]["search"])
	}
	// a host that ignores search answers everything; the name is checked again
	callHost = func(fn string, args, out any) error {
		if fn != sdk.FnEntitiesList {
			return errors.New("unexpected " + fn)
		}
		a := args.(map[string]any)
		delete(a, "search")
		return h.listDrafts(a, out)
	}
	if rows, _, _ := scanDrafts(defaultSettings(), "draft 002", clock().Add(time.Hour)); len(rows) != 10 {
		t.Errorf("a host without search: %d rows", len(rows))
	}
}

func TestDraftOfReadsAttributes(t *testing.T) {
	raw := entityDTO{ID: "d1", Slug: "s", Name: "N", Attributes: []byte(`{"start_at":"2027-04-01T18:00:00","end_at":"2027-04-01T20:00:00","all_day":false,"venue":["v1"],"venue_name":" Hall ","source_kind":"ical","source_uid":"u","time_zone":"America/New_York"}`)}
	d := draftOf(raw, defaultSettings())
	if !d.HasStart || d.Start.UTC().Hour() != 22 || d.End.UTC().Hour() != 0 || d.VenueID != "v1" || d.VenueName != "Hall" || d.SourceKind != "ical" || d.SourceUID != "u" {
		t.Errorf("draft = %+v", d)
	}
	if d := draftOf(entityDTO{ID: "d2", Attributes: []byte(`not json`)}, defaultSettings()); d.HasStart || d.ID != "d2" {
		t.Errorf("unreadable = %+v", d)
	}
}
