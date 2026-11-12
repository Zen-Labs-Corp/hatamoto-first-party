package main

import (
	"encoding/json"
	"net/url"
	"strconv"
	"time"

	"hatamoto.dev/sdk"
)

// Venue addresses become map coordinates through Nominatim, only while the
// "Show map" setting is on. Lookups are cached; a failed one is retried after
// a day. The usage policy (https://operations.osmfoundation.org/policies/nominatim/)
// asks for at most one request per second and an identifying User-Agent.

const (
	nominatimHost  = "nominatim.openstreetmap.org"
	geocodePerTick = 20
	geocodeGap     = time.Second
	failedRetry    = 24 * time.Hour
	fetchTimeout   = 5 * time.Second
	minCall        = time.Second // least time left for which a request starts
	userAgentBase  = "hatamoto-event-calendar/0.2"
)

// sleep waits between two requests; tests replace it.
var sleep = time.Sleep

// fetched is the part of the http.fetch answer this plugin reads.
type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

func userAgent(s settings) string {
	if s.GeocodeContact == "" {
		return userAgentBase
	}
	return userAgentBase + " (+" + s.GeocodeContact + ")"
}

func geocodeURL(addr string) string {
	return "https://" + nominatimHost + "/search?format=jsonv2&limit=1&q=" + url.QueryEscape(addr)
}

// parseNominatim reads the first hit of a search answer.
func parseNominatim(body string) (lat, lon float64, ok bool) {
	var hits []struct {
		Lat string `json:"lat"`
		Lon string `json:"lon"`
	}
	if json.Unmarshal([]byte(body), &hits) != nil || len(hits) == 0 {
		return 0, 0, false
	}
	la, err1 := strconv.ParseFloat(hits[0].Lat, 64)
	lo, err2 := strconv.ParseFloat(hits[0].Lon, 64)
	if err1 != nil || err2 != nil || la < -90 || la > 90 || lo < -180 || lo > 180 {
		return 0, 0, false
	}
	return la, lo, true
}

// addressOf is the one-line address to look up: the venue's, else the text
// fields of the event. Empty when there is nothing to look up.
func addressOf(v Venue, e Event) string {
	if a := joinNonEmpty(v.Address, v.City, v.Region, v.Postcode, v.Country); a != "" {
		return a
	}
	return joinNonEmpty(e.Address, e.VenueCity)
}

// cacheEntry is one geocode_cache row.
type cacheEntry struct {
	Status    string   `json:"status"`
	Lat       *float64 `json:"lat"`
	Lon       *float64 `json:"lon"`
	FetchedAt string   `json:"fetched_at"`
}

// lookupCache reads the cache row of an address; nil when there is none.
func lookupCache(addr string) (*cacheEntry, error) {
	var c *cacheEntry
	err := dbJSON(`SELECT (SELECT jsonb_build_object('status', status, 'lat', latitude, 'lon', longitude, 'fetched_at', fetched_at)
 FROM geocode_cache WHERE address = $1)`, &c, addr)
	return c, err
}

func storeCache(addr, status string, lat, lon any) error {
	return dbExec(`INSERT INTO geocode_cache (address, latitude, longitude, status, fetched_at)
VALUES ($1, $2::double precision, $3::double precision, $4, now())
ON CONFLICT (address) DO UPDATE SET latitude = EXCLUDED.latitude, longitude = EXCLUDED.longitude,
 status = EXCLUDED.status, fetched_at = now()`, addr, lat, lon, status)
}

// cachedCoords is the cached position of an address; false when it is not
// cached, failed or the db is unavailable.
func cachedCoords(addr string) (lat, lon float64, ok bool) {
	if addr == "" {
		return 0, 0, false
	}
	c, err := lookupCache(addr)
	if err != nil || c == nil || c.Status != "ok" || c.Lat == nil || c.Lon == nil {
		return 0, 0, false
	}
	return *c.Lat, *c.Lon, true
}

// needsLookup is false for an address that is cached, or failed less than
// failedRetry ago. A cache that cannot be read counts as "do not look up".
func needsLookup(addr string) bool {
	c, err := lookupCache(addr)
	if err != nil {
		return false
	}
	if c == nil {
		return true
	}
	if c.Status == "ok" {
		return false
	}
	at, err := time.Parse(time.RFC3339, c.FetchedAt)
	return err != nil || now().Sub(at) >= failedRetry
}

// geocodeAddresses looks up the addresses that are not cached, at most
// geocodePerTick of them, one per geocodeGap, and stops once budgetEnd is
// near or a request slot stays taken after one wait. It returns how many it looked up. It does nothing unless ShowMap is on.
func geocodeAddresses(s settings, addrs []string, budgetEnd time.Time) (done int) {
	if !s.ShowMap {
		return 0
	}
	seen := map[string]bool{}
	for _, addr := range addrs {
		if addr == "" || seen[addr] {
			continue
		}
		seen[addr] = true
		if done >= geocodePerTick {
			break
		}
		left := budgetEnd.Sub(clock())
		if left < minCall {
			break
		}
		if !needsLookup(addr) {
			continue
		}
		if done > 0 || !claimSlot() {
			// the previous request, or another instance's, was less than a second
			// ago: wait once, and give up for this run if the slot is still taken
			sleep(geocodeGap)
			if left = budgetEnd.Sub(clock()); left < minCall || !claimSlot() {
				break
			}
		}
		done++
		lookup(s, addr, left)
	}
	return done
}

// throttleKey is the geocode_cache row that holds the time of the last request,
// shared by every instance of the plugin (hooks and cron run in parallel).
const throttleKey = "!last-request"

// claimSlot reserves the right to make one Nominatim request now: true when
// the last one is at least geocodeGap old. It is atomic in the database.
func claimSlot() bool {
	var res struct {
		RowsAffected int `json:"rows_affected"`
	}
	err := callHost(sdk.FnDBExec, map[string]any{"sql": `INSERT INTO geocode_cache (address, status, fetched_at)
VALUES ($1, 'failed', now())
ON CONFLICT (address) DO UPDATE SET fetched_at = now()
 WHERE geocode_cache.fetched_at <= now() - interval '1 second'`, "args": []any{throttleKey}}, &res)
	return err == nil && res.RowsAffected > 0
}

// geocodeOne looks up one address right after an event was saved, so the map
// does not wait for the hourly check. It skips when Show map is off, the
// address is cached or tried recently, another request was made less than a
// second ago (the hourly check picks it up) or there is no time left.
func geocodeOne(s settings, addr string, budgetEnd time.Time) {
	if !s.ShowMap || addr == "" || budgetEnd.Sub(clock()) < minCall || !needsLookup(addr) || !claimSlot() {
		return
	}
	lookup(s, addr, budgetEnd.Sub(clock()))
}

// lookup asks Nominatim for one address and caches the answer, or the failure.
func lookup(s settings, addr string, left time.Duration) {
	var res fetched
	err := callHost(sdk.FnHTTPFetch, map[string]any{
		"method": "GET", "url": geocodeURL(addr), "timeout_ms": int(min(fetchTimeout, left) / time.Millisecond),
		"headers": map[string]string{"User-Agent": userAgent(s), "Accept": "application/json"},
	}, &res)
	lat, lon, ok := parseNominatim(res.Body)
	if err != nil || res.Status != 200 || res.Truncated || !ok {
		_ = storeCache(addr, "failed", nil, nil)
		return
	}
	_ = storeCache(addr, "ok", lat, lon)
}
