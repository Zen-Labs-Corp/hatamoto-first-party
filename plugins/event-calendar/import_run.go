package main

import (
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// Calendar imports. The fetch phase reads one source per step and buffers the
// normalised events; the transform phase (import_stage.go) matches them with
// the events on the site and stages the changes.

const (
	runImportScheduled = "import_scheduled"
	actionImportURL    = "import_url"
	maxImportEvents    = 100 // per run, as many as the standing rule lets through in an hour
	maxSources         = 8
	importFetchMs      = 7000
	maxImportCursor    = 4096

	bufEvent = "ev/"
	bufError = "err/"
	bufStat  = "stat/fetch"
)

// importHosts are the hosts a scheduled import may read, the manifest's
// allowed_hosts without the geocoder and the any-address entry.
var importHosts = []string{
	"calendar.google.com", "outlook.office365.com", "outlook.office.com", "outlook.live.com", "www.meetup.com", "www.eventbrite.com",
}

var (
	scheduledKinds = []string{"google", "outlook", "meetup"}
	importKinds    = []string{"ical", "google", "outlook", "eventbrite", "meetup"}
	nonSlugRe      = regexp.MustCompile(`[^a-z0-9]+`)
)

type importSource struct{ Name, Kind, URL, Category string }

// importSlug is a lower-case, dash-separated form of a name.
func importSlug(s string) string {
	return strings.Trim(nonSlugRe.ReplaceAllString(strings.ToLower(s), "-"), "-")
}

// importKey is the key of one imported event: the source kind, the source name
// and a hash of the feed's UID. It is the event's source_uid and never starts
// with dup-, so it cannot be mistaken for a copy made by this plugin.
func importKey(kind, source, uid string) string {
	name := importSlug(source)
	if len(name) > 24 {
		name = strings.Trim(name[:24], "-")
	}
	sum := sha1.Sum([]byte(uid))
	return kind + "-" + name + "-" + hex.EncodeToString(sum[:])[:16]
}

// loadImportSources reads the import_sources setting: enabled sources of a
// scheduled kind with an https address on one of the fixed hosts, at most
// maxSources. Anything else is ignored.
func loadImportSources() []importSource {
	var raw struct {
		Sources []struct {
			Name     string `json:"name"`
			Kind     string `json:"kind"`
			URL      string `json:"url"`
			Category string `json:"category"`
			Enabled  *bool  `json:"enabled"`
		} `json:"import_sources"`
	}
	if callHost(sdk.FnSettingsGet, struct{}{}, &raw) != nil {
		return nil
	}
	var out []importSource
	for _, r := range raw.Sources {
		if len(out) >= maxSources {
			break
		}
		if r.Enabled != nil && !*r.Enabled || !slices.Contains(scheduledKinds, r.Kind) {
			continue
		}
		u, err := normaliseFeedURL(r.URL)
		if err != nil || !strings.HasPrefix(u, "https://") || !slices.Contains(importHosts, hostOf(u)) {
			continue
		}
		name := cleanText(r.Name, 80, false)
		if name == "" {
			name = hostOf(u)
		}
		out = append(out, importSource{Name: name, Kind: r.Kind, URL: u, Category: cleanText(r.Category, 80, false)})
	}
	return out
}

// importRecord is what the fetch phase buffers for one event.
type importRecord struct {
	Source   string      `json:"source"`
	Kind     string      `json:"kind"`
	Category string      `json:"category"`
	Key      string      `json:"key"`
	Ev       importEvent `json:"ev"`
}

type importCursor struct {
	Src    int `json:"src"`    // next source to read
	N      int `json:"n"`      // events buffered
	Over   int `json:"over"`   // events left out by the cap
	Failed int `json:"failed"` // sources that could not be read
}

// fetchPhase routes the fetch phase: the import runs read calendars, every
// other run has nothing to fetch.
func fetchPhase(s sdk.RunStep) (sdk.RunStepResult, error) {
	info, err := guest.RunInfo(callHost)
	if err != nil {
		return failRun("The run inputs could not be read.")
	}
	if info.Name == runImportScheduled || info.Name == runEditDates && importAction(info) {
		return importFetchStep(s, info)
	}
	return sdk.RunStepResult{}, guest.RunFinish(callHost, nil)
}

func importAction(info sdk.RunInfo) bool {
	var p map[string]any
	_ = json.Unmarshal(info.Params, &p)
	return paramString(p, "action") == actionImportURL
}

// sourcesFor lists what this run reads: the settings for the scheduled run,
// the one address of the form otherwise.
func sourcesFor(info sdk.RunInfo) ([]importSource, string) {
	if info.Name == runImportScheduled {
		return loadImportSources(), ""
	}
	var p map[string]any
	_ = json.Unmarshal(info.Params, &p)
	u, err := normaliseFeedURL(paramString(p, "url"))
	if err != nil {
		return nil, "The calendar address must start with https:// or webcal://."
	}
	kind := paramString(p, "kind")
	if kind == "" {
		kind = "ical"
	}
	if !slices.Contains(importKinds, kind) {
		return nil, "Choose the kind of calendar: ical, google, outlook, eventbrite or meetup."
	}
	name := cleanText(paramString(p, "name"), 80, false)
	if name == "" {
		name = hostOf(u)
	}
	return []importSource{{Name: name, Kind: kind, URL: u}}, ""
}

// importFetchStep reads the next source, buffers its events and checkpoints.
// A source that cannot be read is noted and skipped; the run fails only when
// no source could be read at all.
func importFetchStep(s sdk.RunStep, info sdk.RunInfo) (sdk.RunStepResult, error) {
	var cur importCursor
	if s.Cursor != "" && json.Unmarshal([]byte(s.Cursor), &cur) != nil {
		return failRun("The run cursor could not be read.")
	}
	sources, reason := sourcesFor(info)
	if reason != "" {
		return failRun(reason)
	}
	if cur.Src < len(sources) {
		src := sources[cur.Src]
		if err := readSource(&cur, src, loadSettings()); err != nil {
			return failRun("The run buffer could not be written. Try again.")
		}
		cur.Src++
	}
	stat, _ := json.Marshal(map[string]int{"over": cur.Over})
	if err := guest.BufferPut(callHost, bufStat, stat); err != nil {
		return failRun("The run buffer could not be written. Try again.")
	}
	if cur.Src < len(sources) {
		cp, _ := json.Marshal(cur)
		if len(cp) > maxImportCursor || guest.RunCheckpoint(callHost, string(cp)) != nil {
			return failRun("The run could not save its place. Try again.")
		}
		_ = guest.RunProgress(callHost, cur.Src, len(sources), fmt.Sprintf("%d of %d calendars read", cur.Src, len(sources)))
		return sdk.RunStepResult{}, nil
	}
	if len(sources) > 0 && cur.Failed == len(sources) {
		return failRun("No calendar could be read. Check the address and the hosts ticked for this run.")
	}
	return sdk.RunStepResult{}, guest.RunFinish(callHost, map[string]any{"phase": "fetch", "events": cur.N})
}

// readSource fetches and parses one source and buffers its events. It returns
// an error only when the run buffer fails; a bad source is counted in cur.
func readSource(cur *importCursor, src importSource, s settings) error {
	events, why := fetchEvents(src, s)
	if why != "" {
		cur.Failed++
		note, _ := json.Marshal(map[string]string{"source": src.Name, "reason": why})
		return guest.BufferPut(callHost, fmt.Sprintf("%s%02d", bufError, cur.Src), note)
	}
	for _, ev := range events {
		key := importKey(src.Kind, src.Name, ev.UID)
		if _, found, err := guest.BufferGet(callHost, bufEvent+key); err != nil {
			return err
		} else if found {
			continue // the same UID twice in one source
		}
		if cur.N >= maxImportEvents {
			cur.Over++
			continue
		}
		rec, _ := json.Marshal(importRecord{Source: src.Name, Kind: src.Kind, Category: src.Category, Key: key, Ev: ev})
		if err := guest.BufferPut(callHost, bufEvent+key, rec); err != nil {
			return err
		}
		cur.N++
	}
	return nil
}

// fetchEvents reads one address. The reason is a short text without the
// address, "" when the source was read.
func fetchEvents(src importSource, s settings) ([]importEvent, string) {
	var res fetched
	err := callHost(sdk.FnHTTPFetch, map[string]any{
		"method": "GET", "url": src.URL, "timeout_ms": importFetchMs,
		"headers": map[string]string{"User-Agent": "hatamoto-event-calendar", "Accept": "text/calendar, text/html;q=0.8"},
	}, &res)
	switch {
	case err != nil:
		return nil, "the address could not be fetched"
	case res.Truncated:
		return nil, "the answer was too large"
	case res.Status != 200:
		return nil, fmt.Sprintf("the answer was status %d", res.Status)
	}
	loc := s.location()
	if strings.Contains(res.Body, "BEGIN:VCALENDAR") {
		events, err := parseICalEvents(res.Body, loc)
		if err != nil {
			return nil, "the answer was not a calendar"
		}
		return events, ""
	}
	events := parseJSONLDEvents(res.Body, loc)
	if len(events) == 0 {
		return nil, "no event data was found"
	}
	return events, ""
}
