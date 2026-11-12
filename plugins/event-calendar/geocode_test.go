package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"
	"time"
)

type cacheRow struct {
	status    string
	lat, lon  *float64
	fetchedAt string
}

// geoHost fakes http.fetch and the geocode_cache table.
type geoHost struct {
	cache    map[string]*cacheRow
	fetches  []map[string]any
	body     string
	status   int
	fetchErr error
}

func newGeoHost(t *testing.T) *geoHost {
	t.Helper()
	h := &geoHost{cache: map[string]*cacheRow{}, body: `[{"lat":"51.5","lon":"-0.1"}]`, status: 200}
	callHost = func(fn string, args, out any) error {
		a := args.(map[string]any)
		switch fn {
		case "db.query":
			row := h.cache[a["args"].([]any)[0].(string)]
			if row == nil {
				return json.Unmarshal([]byte(`{"rows":[[null]]}`), out)
			}
			cell, _ := json.Marshal(map[string]any{"status": row.status, "lat": row.lat, "lon": row.lon, "fetched_at": row.fetchedAt})
			return json.Unmarshal([]byte(`{"rows":[[`+string(cell)+`]]}`), out)
		case "db.exec":
			p := a["args"].([]any)
			row := &cacheRow{status: p[3].(string), fetchedAt: now().UTC().Format(time.RFC3339)}
			if f, ok := p[1].(float64); ok {
				row.lat = &f
			}
			if f, ok := p[2].(float64); ok {
				row.lon = &f
			}
			h.cache[p[0].(string)] = row
			return nil
		case "http.fetch":
			h.fetches = append(h.fetches, a)
			if h.fetchErr != nil {
				return h.fetchErr
			}
			return json.Unmarshal([]byte(fmt.Sprintf(`{"status":%d,"body":%q}`, h.status, h.body)), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	sleep = func(time.Duration) {}
	now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC) }
	clock = time.Now
	t.Cleanup(func() { sleep = time.Sleep; now = time.Now; clock = time.Now })
	return h
}

func mapOn() settings { return settings{ShowMap: true} }

func far() time.Time { return time.Now().Add(time.Hour) }

func TestParseNominatim(t *testing.T) {
	if lat, lon, ok := parseNominatim(`[{"lat":"51.5","lon":"-0.1","display_name":"x"}]`); !ok || lat != 51.5 || lon != -0.1 {
		t.Errorf("got %v %v %v", lat, lon, ok)
	}
	for _, bad := range []string{``, `[]`, `{}`, `[{"lat":"x","lon":"1"}]`, `[{"lat":"91","lon":"1"}]`, `[{"lat":1,"lon":2}]`, `not json`} {
		if _, _, ok := parseNominatim(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestGeocodeCachesAndSkips(t *testing.T) {
	h := newGeoHost(t)
	if n := geocodeAddresses(mapOn(), []string{"1 High St, London", "1 High St, London", ""}, far()); n != 1 {
		t.Fatalf("done = %d", n)
	}
	if n := geocodeAddresses(mapOn(), []string{"1 High St, London"}, far()); n != 0 || len(h.fetches) != 1 {
		t.Fatalf("second run: done = %d, fetches = %d", n, len(h.fetches))
	}
	lat, lon, ok := cachedCoords("1 High St, London")
	if !ok || lat != 51.5 || lon != -0.1 {
		t.Errorf("cached = %v %v %v", lat, lon, ok)
	}
	if _, _, ok := cachedCoords("elsewhere"); ok {
		t.Error("uncached address has coords")
	}
	u, err := url.Parse(h.fetches[0]["url"].(string))
	if err != nil || u.Host != nominatimHost || u.Scheme != "https" || u.Query().Get("q") != "1 High St, London" {
		t.Errorf("url = %v", h.fetches[0]["url"])
	}
}

func TestGeocodeFailedRetryAfter24h(t *testing.T) {
	h := newGeoHost(t)
	h.status = 500
	geocodeAddresses(mapOn(), []string{"a"}, far())
	if h.cache["a"] == nil || h.cache["a"].status != "failed" || h.cache["a"].lat != nil {
		t.Fatalf("cache = %+v", h.cache["a"])
	}
	if _, _, ok := cachedCoords("a"); ok {
		t.Error("failed lookup has coords")
	}
	h.status = 200
	h.cache["a"].fetchedAt = now().Add(-23 * time.Hour).Format(time.RFC3339)
	if n := geocodeAddresses(mapOn(), []string{"a"}, far()); n != 0 {
		t.Errorf("retried after 23 h: %d", n)
	}
	h.cache["a"].fetchedAt = now().Add(-25 * time.Hour).Format(time.RFC3339)
	if n := geocodeAddresses(mapOn(), []string{"a"}, far()); n != 1 || h.cache["a"].status != "ok" {
		t.Errorf("not retried after 25 h: %d %+v", n, h.cache["a"])
	}
}

func TestGeocodeFailuresAreCached(t *testing.T) {
	h := newGeoHost(t)
	h.fetchErr = errors.New("net down")
	geocodeAddresses(mapOn(), []string{"a"}, far())
	h.fetchErr = nil
	h.body = `[]`
	geocodeAddresses(mapOn(), []string{"b"}, far())
	h.body, h.status = `[{"lat":"1","lon":"2"}]`, 200
	if h.cache["a"].status != "failed" || h.cache["b"].status != "failed" {
		t.Errorf("cache = %+v %+v", h.cache["a"], h.cache["b"])
	}
}

func TestGeocodeRespectsShowMap(t *testing.T) {
	h := newGeoHost(t)
	if n := geocodeAddresses(settings{}, []string{"a"}, far()); n != 0 || len(h.fetches) != 0 {
		t.Fatalf("done = %d, fetches = %d", n, len(h.fetches))
	}
}

func TestGeocodeUserAgent(t *testing.T) {
	h := newGeoHost(t)
	geocodeAddresses(settings{ShowMap: true, GeocodeContact: "maps@example.org"}, []string{"a"}, far())
	geocodeAddresses(mapOn(), []string{"b"}, far())
	ua := func(i int) any { return h.fetches[i]["headers"].(map[string]string)["User-Agent"] }
	if ua(0) != "hatamoto-event-calendar/0.2 (+maps@example.org)" || ua(1) != "hatamoto-event-calendar/0.2" {
		t.Errorf("user agents = %v, %v", ua(0), ua(1))
	}
}

func TestGeocodeLimit20(t *testing.T) {
	h := newGeoHost(t)
	var addrs []string
	for i := 0; i < 30; i++ {
		addrs = append(addrs, fmt.Sprint("addr ", i))
	}
	slept := 0
	sleep = func(d time.Duration) {
		if d != geocodeGap {
			t.Errorf("gap = %v", d)
		}
		slept++
	}
	if n := geocodeAddresses(mapOn(), addrs, far()); n != geocodePerTick || len(h.fetches) != geocodePerTick {
		t.Fatalf("done = %d, fetches = %d", n, len(h.fetches))
	}
	if slept != geocodePerTick-1 {
		t.Errorf("sleeps = %d", slept)
	}
	for _, f := range h.fetches {
		if !strings.HasPrefix(f["url"].(string), "https://"+nominatimHost+"/") {
			t.Errorf("url = %v", f["url"])
		}
	}
}

func TestGeocodeBudgetStops(t *testing.T) {
	h := newGeoHost(t)
	at := time.Date(2026, 6, 1, 12, 0, 0, 0, time.UTC)
	clock = func() time.Time { return at }
	sleep = func(d time.Duration) { at = at.Add(d + 400*time.Millisecond) } // each request takes 400 ms
	end := at.Add(3500 * time.Millisecond)
	n := geocodeAddresses(mapOn(), []string{"a", "b", "c", "d", "e", "f"}, end)
	if n < 1 || n >= 6 || len(h.fetches) != n {
		t.Fatalf("done = %d, fetches = %d", n, len(h.fetches))
	}
	for _, f := range h.fetches {
		if ms := f["timeout_ms"].(int); ms <= 0 || ms > 5000 {
			t.Errorf("timeout_ms = %d", ms)
		}
	}
	if n := geocodeAddresses(mapOn(), []string{"z"}, at.Add(500*time.Millisecond)); n != 0 {
		t.Errorf("started a request with no time left: %d", n)
	}
}

func TestGeocodeDBDownMakesNoRequest(t *testing.T) {
	var fetches int
	callHost = func(fn string, _, _ any) error {
		if fn == "http.fetch" {
			fetches++
		}
		return errors.New("no db")
	}
	if n := geocodeAddresses(mapOn(), []string{"a"}, far()); n != 0 || fetches != 0 {
		t.Errorf("done = %d, fetches = %d", n, fetches)
	}
}

func TestGeocodeAddressOf(t *testing.T) {
	v := Venue{Address: "1 High St", City: "London", Postcode: "N1", Country: "UK"}
	if got := addressOf(v, Event{Address: "ignored"}); got != "1 High St, London, N1, UK" {
		t.Errorf("addressOf = %q", got)
	}
	if got := addressOf(Venue{}, Event{Address: "2 Low Rd", VenueCity: "Leeds"}); got != "2 Low Rd, Leeds" {
		t.Errorf("addressOf = %q", got)
	}
	if got := addressOf(Venue{}, Event{}); got != "" {
		t.Errorf("addressOf = %q", got)
	}
}
