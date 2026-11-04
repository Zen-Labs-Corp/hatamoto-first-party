package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// hookBudget is how much of the 10 s hook time the plugin spends on provider
// calls; the rest is left for the host round trips around them.
const hookBudget = 8 * time.Second

const statusPublished = "published"

// entity is the part of the host's Entity this plugin reads (SDK 0.11 has no
// Entity type).
type entity struct {
	ID       string `json:"id"`
	Slug     string `json:"slug"`
	Status   string `json:"status"`
	Path     string `json:"path"`
	URL      string `json:"url"`
	ParentID string `json:"parent_id"`
}

// handle runs one entity action. It never returns an error: a hook must not
// fail because of its own settings, the host or the provider.
func handle(trigger string, ev sdk.EntityEvent) {
	s, err := loadSettings()
	if err != nil {
		logf("warn", "cdn-purge: settings unavailable: %v", err)
		return
	}
	if !s.configured() {
		return
	}
	budgetEnd = time.Now().Add(hookBudget)
	switch trigger {
	case "published":
		onPublished(s, ev)
	case "updated":
		onUpdated(s, ev)
	case "deleted":
		onDeleted(s, ev)
	}
}

func getEntity(id string) (e entity, found bool, err error) {
	err = callHost(sdk.FnEntitiesGet, map[string]string{"id": id}, &e)
	var he *sdk.HostError
	if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
		return e, false, nil
	}
	return e, err == nil, err
}

func kvKey(id string) string { return "url:" + id }

// kvURL returns the last public URL stored for an entity, "" when none.
func kvURL(id string) string {
	var got struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost(sdk.FnKVGet, map[string]string{"key": kvKey(id)}, &got); err != nil || !got.Found {
		return ""
	}
	var v string
	_ = json.Unmarshal(got.Value, &v)
	return v
}

func kvStore(id, rawURL string) {
	if err := callHost(sdk.FnKVSet, map[string]any{"key": kvKey(id), "value": rawURL}, nil); err != nil {
		logf("warn", "cdn-purge: could not remember the URL of %s: %v", id, err)
	}
}

func kvForget(id string) {
	if err := callHost(sdk.FnKVDelete, map[string]string{"key": kvKey(id)}, nil); err != nil {
		logf("warn", "cdn-purge: could not forget the URL of %s: %v", id, err)
	}
}

func onPublished(s settings, ev sdk.EntityEvent) {
	e, found, err := getEntity(ev.ID)
	if err != nil || !found {
		logf("warn", "cdn-purge: entity %s not readable, nothing purged", ev.ID)
		return
	}
	old := kvURL(ev.ID)
	page, origin := pageURL(e, s, old)
	if page == "" {
		purgeOrSkip("published", s, nil, false)
		return
	}
	purgeOrSkip("published", s, collect(origin, s, page, old), true)
	kvStore(ev.ID, page)
}

func onUpdated(s settings, ev sdk.EntityEvent) {
	e, found, err := getEntity(ev.ID)
	if err != nil {
		logf("warn", "cdn-purge: entity %s not readable, nothing purged", ev.ID)
		return
	}
	old := kvURL(ev.ID)
	if found && e.Status == statusPublished {
		page, origin := pageURL(e, s, old)
		if page == "" {
			purgeOrSkip("updated", s, nil, false)
			return
		}
		purgeOrSkip("updated", s, collect(origin, s, page, old), true)
		kvStore(ev.ID, page)
		return
	}
	// Unpublished (or gone): purge what was public and forget it. Without a
	// remembered URL (published before the plugin was installed) the entity's
	// own URL is purged, as it is still readable.
	if !found && old == "" {
		return
	}
	origin := pickOrigin(s, old)
	var urls []string
	if found {
		if page, o := pageURL(e, s, old); page != "" {
			urls, origin = append(urls, page), o
		}
	}
	purgeOrSkip("updated", s, collect(origin, s, append(urls, old)...), origin != "")
	if old != "" {
		kvForget(ev.ID)
	}
}

func onDeleted(s settings, ev sdk.EntityEvent) {
	old := kvURL(ev.ID)
	origin := pickOrigin(s, old)
	purgeOrSkip("deleted", s, collect(origin, s, old), origin != "")
	if old != "" {
		kvForget(ev.ID)
	}
}

// collect joins the given absolute URLs with the also_purge paths, without
// duplicates and without empty entries.
func collect(origin string, s settings, urls ...string) []string {
	return dedupe(append(urls, resolve(origin, s.AlsoPurge)...))
}

func dedupe(urls []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, u := range urls {
		if u != "" && !seen[u] {
			seen[u] = true
			out = append(out, u)
		}
	}
	return out
}

// originOf returns scheme://host of an absolute http(s) URL, "" otherwise.
func originOf(raw string) string {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return u.Scheme + "://" + u.Host
}

// pickOrigin takes the origin of the first absolute URL among hints, else the
// Site URL setting, else "".
func pickOrigin(s settings, hints ...string) string {
	for _, h := range hints {
		if o := originOf(h); o != "" {
			return o
		}
	}
	return originOf(s.SiteURL)
}

// pageURL is the absolute public URL of the entity and its origin; "" when the
// host gave no path or no origin is known.
func pageURL(e entity, s settings, hints ...string) (page, origin string) {
	origin = pickOrigin(s, append([]string{e.URL}, hints...)...)
	if originOf(e.URL) != "" {
		return e.URL, origin
	}
	path := e.Path
	if path == "" {
		path = e.URL
	}
	if origin == "" || path == "" {
		return "", origin
	}
	return origin + ensureSlash(path), origin
}

func ensureSlash(p string) string {
	if strings.HasPrefix(p, "/") {
		return p
	}
	return "/" + p
}

// resolve joins paths to the origin, keeping order and dropping duplicates. A
// full http(s) URL is kept as it is.
func resolve(origin string, paths []string) []string {
	var out []string
	for _, p := range paths {
		switch {
		case originOf(p) != "":
			out = append(out, p)
		case origin != "":
			out = append(out, origin+ensureSlash(p))
		}
	}
	return dedupe(out)
}

// purgeOrSkip purges urls, or everything when the toggle is on. Without an
// origin (and no toggle) it records one "set Site URL" row and purges nothing.
func purgeOrSkip(trigger string, s settings, urls []string, haveOrigin bool) {
	if s.PurgeEverything {
		runPurge(trigger, s, nil)
		return
	}
	if !haveOrigin {
		logf("warn", "cdn-purge: nothing purged on %s: set Site URL in the plugin settings", trigger)
		insertLog(trigger, s.Provider, nil, 0, "set Site URL")
		return
	}
	runPurge(trigger, s, urls)
}

// runPurge sends the purge and writes one log row per request batch, and one
// log line per failed batch.
func runPurge(trigger string, s settings, urls []string) {
	var results []batchResult
	if s.PurgeEverything {
		results = []batchResult{purgeAll(s)}
	} else {
		results = purgeURLs(s, urls)
	}
	for _, r := range results {
		insertLog(trigger, s.Provider, r.URLs, r.Status, r.Err)
		if !r.ok() {
			logf("error", "cdn-purge: %s purge on %s failed: %s", s.Provider, trigger, r.Err)
		}
	}
}
