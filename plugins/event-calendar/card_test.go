package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const cardEvent = `{"id":"c1","schema_slug":"calendar-event","slug":"spring-jazz","name":"Spring Jazz Night","status":"published","url":"https://example.org/events/spring-jazz",
 "attributes":{"start_at":"2027-02-14T19:00:00Z","end_at":"2027-02-14T21:30:00Z","image":"/media/jazz.jpg","description":"DESC",
 "venue":["v1"],"organizers":["o1"],"series":["s1"],"category":["k1"],"attendance_mode":"hybrid","online_url":"https://example.org/join",
 "ticket_url":"https://example.org/tickets","price_amount":12,"currency":"GBP"}}`

var cardRefs = []string{
	venueDoc, orgDoc,
	`{"id":"s1","schema_slug":"calendar-series","name":"Winter season","status":"published","url":"https://example.org/series/winter"}`,
	`{"id":"k1","schema_slug":"calendar-category","name":"Jazz","status":"published"}`,
}

// cardHost installs a fake host with the given settings JSON and returns it.
func cardHost(t *testing.T, settingsJSON string, entities ...string) *fakeHost {
	t.Helper()
	h := &fakeHost{entities: entities}
	h.install(t)
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if fn == "settings.get" {
			return jsonInto(settingsJSON, out)
		}
		return inner(fn, args, out)
	}
	return h
}

func runCard(t *testing.T, entityID string) sdk.BuilderDataResult {
	t.Helper()
	got := runBuilder(t, sdk.BuilderDataRequest{EntityID: entityID, Requests: []sdk.BuilderDataQuery{{ID: "e", Source: sourceEvent, Limit: 1}}})
	if len(got.Results) != 1 || got.Results[0].ID != "e" {
		t.Fatalf("results = %+v", got.Results)
	}
	return got.Results[0]
}

func TestEventSourceAttrs(t *testing.T) {
	doc := strings.Replace(cardEvent, "DESC", strings.Repeat("x", 5000), 1)
	cardHost(t, `{}`, append([]string{doc}, cardRefs...)...)
	res := runCard(t, "c1")
	if len(res.Items) != 1 {
		t.Fatalf("items = %d", len(res.Items))
	}
	it := res.Items[0]
	if len(it.Attrs) > sdk.MaxBuilderDataAttrs {
		t.Errorf("%d attrs", len(it.Attrs))
	}
	check := func(name, v string) {
		if len(v) > sdk.MaxBuilderDataString || !utf8.ValidString(v) {
			t.Errorf("%s: %d bytes, valid=%v", name, len(v), utf8.ValidString(v))
		}
	}
	for k, v := range it.Attrs {
		check(k, v)
	}
	for k, v := range map[string]string{"title": it.Title, "url": it.URL, "excerpt": it.Excerpt, "image": it.Image, "category": it.Category, "author": it.Author, "author_url": it.AuthorURL} {
		check(k, v)
	}
	if len(it.Excerpt) != sdk.MaxBuilderDataString {
		t.Errorf("long description not capped to %d: %d", sdk.MaxBuilderDataString, len(it.Excerpt))
	}
	if it.Title != "Spring Jazz Night" || it.URL != "https://example.org/events/spring-jazz" || it.Category != "Jazz" ||
		it.Author != "Jazz Society" || it.AuthorURL != "https://example.org" || it.PublishedAt != "2027-02-14T19:00:00Z" {
		t.Errorf("item = %+v", it)
	}
	a := it.Attrs
	for k, want := range map[string]string{
		"status": "Scheduled", "venue": "Old Mill, 12 High St, Leeds", "organizer_email": "hi@example.org", "price": "12 GBP",
		"online_url": "https://example.org/join", "series": "Winter season", "series_url": "https://example.org/series/winter",
		"ticket_url": "https://example.org/tickets", "ics_url": "/p/event-calendar/event/spring-jazz.ics",
	} {
		if a[k] != want {
			t.Errorf("attrs[%s] = %q, want %q", k, a[k], want)
		}
	}
	for _, k := range []string{"when", "google_url", "outlook_url", "yahoo_url"} {
		if a[k] == "" {
			t.Errorf("attrs[%s] empty", k)
		}
	}
	if _, ok := a["map_url"]; ok {
		t.Error("map_url with the map off")
	}
}

func TestEventSourceEmptyOnNonEntityPage(t *testing.T) {
	h := cardHost(t, `{}`, append([]string{cardEvent}, cardRefs...)...)
	calls := 0
	inner := callHost
	callHost = func(fn string, args, out any) error { calls++; return inner(fn, args, out) }
	if res := runCard(t, ""); len(res.Items) != 0 || res.Error != "" || calls != 0 {
		t.Errorf("no entity: %+v, %d host calls", res, calls)
	}
	h.entities = append(h.entities, `{"id":"pg","schema_slug":"page","slug":"pg","name":"Page","status":"published","attributes":{"start_at":"2027-01-01"}}`,
		`{"id":"dr","schema_slug":"calendar-event","slug":"dr","name":"Draft","status":"draft","attributes":{"start_at":"2027-01-01"}}`)
	for _, id := range []string{"pg", "dr", "missing"} {
		if res := runCard(t, id); len(res.Items) != 0 || res.Error != "" {
			t.Errorf("%s: %+v", id, res)
		}
	}
}

func TestEventSourceMapOnlyWhenEnabled(t *testing.T) {
	docs := append([]string{cardEvent}, cardRefs...)
	cardHost(t, `{}`, docs...)
	if _, ok := runCard(t, "c1").Items[0].Attrs["map_url"]; ok {
		t.Error("map_url with show_map off")
	}
	cardHost(t, `{"show_map":true}`, docs...)
	got := runCard(t, "c1").Items[0].Attrs["map_url"]
	if want := "https://www.openstreetmap.org/?mlat=53.80000&mlon=-1.55000#map=16/53.80000/-1.55000"; got != want {
		t.Errorf("map_url = %q", got)
	}
	// no venue coordinates: the cached lookup of the address answers
	noGeo := strings.Replace(venueDoc, `"latitude":53.8,"longitude":-1.55`, `"city":"Leeds"`, 1)
	h := cardHost(t, `{"show_map":true}`, append([]string{cardEvent, noGeo}, cardRefs[1:]...)...)
	if _, ok := runCard(t, "c1").Items[0].Attrs["map_url"]; ok {
		t.Error("map_url without coordinates")
	}
	h.dbRows = `{"status":"ok","lat":51.5,"lon":-0.12,"fetched_at":"2026-01-01T00:00:00Z"}`
	if got := runCard(t, "c1").Items[0].Attrs["map_url"]; !strings.Contains(got, "mlat=51.50000&mlon=-0.12000") {
		t.Errorf("cached map_url = %q", got)
	}
}

func TestEventSourceTimeBudget(t *testing.T) {
	h := cardHost(t, `{"show_map":true}`, append([]string{cardEvent}, cardRefs...)...)
	at := time.Now()
	clock = func() time.Time { at = at.Add(builderBudget); return at } // every look at the clock burns the budget
	t.Cleanup(func() { clock = time.Now })
	res := runCard(t, "c1")
	if len(res.Items) != 1 || res.Items[0].Title != "Spring Jazz Night" {
		t.Fatalf("res = %+v", res)
	}
	if len(h.getCalls) != 1 {
		t.Errorf("%d entity reads, want only the event", len(h.getCalls))
	}
	a := res.Items[0].Attrs
	if a["organizer_email"] != "" || a["series"] != "" || a["map_url"] != "" || a["when"] == "" || a["google_url"] == "" {
		t.Errorf("attrs = %v", a)
	}
}

func jsonInto(raw string, out any) error { return json.Unmarshal([]byte(raw), out) }

const cardEventZone = `{"id":"z1","schema_slug":"calendar-event","slug":"summer","name":"Summer night","status":"published",
 "attributes":{"start_at":"2027-07-10T18:00:00Z","time_zone":"Europe/London"}}`

// The card shows the time in the event's own zone and names the zone.
func TestEventCardNamesTheTimeZone(t *testing.T) {
	cardHost(t, `{}`, cardEventZone, strings.Replace(cardEvent, `"c1"`, `"c2"`, 1))
	if got := runCard(t, "z1").Items[0].Attrs["when"]; got != "Sat 10 Jul 2027, 7:00 pm (Europe/London)" {
		t.Errorf("when = %q", got)
	}
	// no zone on the event: the site zone is named
	if got := runCard(t, "c2").Items[0].Attrs["when"]; !strings.HasSuffix(got, " (UTC)") {
		t.Errorf("when = %q", got)
	}
}

func TestEventCardLinksToThePluginPages(t *testing.T) {
	venue := strings.Replace(venueDoc, `"name":"Old Mill"`, `"slug":"old-mill","name":"Old Mill"`, 1)
	org := strings.Replace(orgDoc, `"name":"Jazz Society"`, `"slug":"jazz-society","name":"Jazz Society"`, 1)
	series := strings.Replace(cardRefs[2], `"name":"Winter season"`, `"slug":"winter","name":"Winter season"`, 1)
	cardHost(t, `{}`, cardEvent, venue, org, series, cardRefs[3])
	a := runCard(t, "c1").Items[0].Attrs
	for k, want := range map[string]string{
		"venue_page": "/p/event-calendar/venue/old-mill", "organizer_page": "/p/event-calendar/organizer/jazz-society",
		"series_page": "/p/event-calendar/series/winter", "series_url": "https://example.org/series/winter",
	} {
		if a[k] != want {
			t.Errorf("attrs[%s] = %q, want %q", k, a[k], want)
		}
	}

	// Entities without a slug get no link, and a slug that is not one is not linked.
	cardHost(t, `{}`, append([]string{cardEvent}, cardRefs...)...)
	for _, k := range []string{"venue_page", "organizer_page", "series_page"} {
		if v, ok := runCard(t, "c1").Items[0].Attrs[k]; ok {
			t.Errorf("attrs[%s] = %q without a slug", k, v)
		}
	}
	bad := strings.Replace(venue, `"slug":"old-mill"`, `"slug":"../x"`, 1)
	cardHost(t, `{}`, cardEvent, bad, org, series, cardRefs[3])
	if v, ok := runCard(t, "c1").Items[0].Attrs["venue_page"]; ok {
		t.Errorf("venue_page = %q for a bad slug", v)
	}

	// A card that is full already keeps its keys and never grows past the limit.
	full := strings.Replace(venue, `"status":"published"`, `"url":"https://example.org/venues/mill","status":"published"`, 1)
	cardHost(t, `{"show_map":true}`, cardEvent, full, org, series, cardRefs[3])
	a = runCard(t, "c1").Items[0].Attrs
	if len(a) != sdk.MaxBuilderDataAttrs || a["map_url"] == "" || a["venue_url"] == "" || a["yahoo_url"] == "" {
		t.Errorf("full card: %d attrs %v", len(a), a)
	}
}
