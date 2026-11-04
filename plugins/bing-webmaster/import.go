package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	// importBudget is how long one import call works, measured from its start;
	// the plugin's call limit is 10 s.
	importBudget = 7 * time.Second
	// endpointReserve is the least time left for which another endpoint is started.
	endpointReserve = 2 * time.Second
)

// endpoints is the import order. Each one is a single fetch and a single store
// step, so a run that stops early only ever repeats whole endpoints, which the
// upserts and snapshot replaces make harmless.
var endpoints = []string{"traffic", "queries", "pages", "crawl", "feeds"}

// clock is the time source of the cron; tests set it.
var clock = time.Now

type importResult struct {
	Done      bool     `json:"done"`
	Remaining []string `json:"remaining"`
	Rows      int      `json:"rows"`
	Error     string   `json:"error,omitempty"` // what failed in endpoints that were tried
}

func init() {
	guest.OnCron("import", onImport)
}

// onImport is the cron. It fails open: whatever Bing or the settings say, the
// run is recorded and logged, and only a failing host call is an error.
func onImport(sdk.CronEvent) error {
	s, err := loadSettings()
	if err != nil {
		return err
	}
	res, err := runImport(s, clock().Add(importBudget), clock)
	switch {
	case errors.Is(err, errNoKey):
		return nil // nothing to import yet
	case err != nil:
		logf("warn", "import: %v", err)
	case res.Error != "":
		logf("warn", "import: %s", res.Error)
	}
	return nil
}

func loadCursor() []string {
	var got struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	var left []string
	if callHost("kv.get", map[string]string{"key": "cursor"}, &got) != nil || !got.Found ||
		json.Unmarshal(got.Value, &left) != nil || len(left) == 0 {
		return slices.Clone(endpoints)
	}
	var out []string
	for _, e := range endpoints { // canonical order; unknown names mean a corrupt entry
		if slices.Contains(left, e) {
			out = append(out, e)
		}
	}
	if len(out) != len(left) {
		return slices.Clone(endpoints)
	}
	return out
}

func saveCursor(left []string) error {
	if len(left) == 0 {
		return callHost("kv.delete", map[string]string{"key": "cursor"}, nil)
	}
	return callHost("kv.set", map[string]any{"key": "cursor", "value": left}, nil)
}

// runEndpoint fetches one endpoint and stores it, returning the rows stored.
func runEndpoint(s settings, name, site string, at time.Time) (int, error) {
	switch name {
	case "traffic":
		rows, err := trafficStats(s, site)
		if err != nil {
			return 0, err
		}
		return len(rows), storeTraffic(rows)
	case "queries":
		rows, err := queryStats(s, site)
		if err != nil {
			return 0, err
		}
		return len(rows), storeStats("query_stats", rows)
	case "pages":
		rows, err := pageStats(s, site)
		if err != nil {
			return 0, err
		}
		return len(rows), storeStats("page_stats", rows)
	case "crawl":
		rows, err := crawlIssues(s, site)
		if err != nil {
			return 0, err
		}
		return len(rows), replaceCrawlIssues(rows, at)
	case "feeds":
		rows, err := feeds(s, site)
		if err != nil {
			return 0, err
		}
		return len(rows), replaceFeeds(rows, at)
	}
	return 0, errors.New("unknown endpoint " + name)
}

// runImport imports the endpoints left from the last run until deadline. The
// error is for a run that cannot start or must stop (no key, no site, key
// rejected); an endpoint that fails is reported in the result, the others
// still run. Old snapshots are only replaced by a successful fetch.
func runImport(s settings, deadline time.Time, now func() time.Time) (importResult, error) {
	res := importResult{Remaining: []string{}}
	if s.APIKey == "" {
		return res, errNoKey
	}
	started := now()
	budgetEnd = deadline
	defer func() { budgetEnd = time.Time{} }()

	finish := func(runErr error) {
		if err := recordRun(started, now(), runErr); err != nil {
			logf("warn", "import log: %v", err)
		}
	}
	site, _, err := resolveSite(s)
	if err != nil {
		finish(err)
		return res, err
	}
	left := loadCursor()
	var failed []string
	for len(left) > 0 {
		if time.Until(deadline) < endpointReserve {
			break
		}
		n, err := runEndpoint(s, left[0], site, now())
		if errors.Is(err, errKeyDenied) {
			finish(err)
			return res, err
		}
		if errors.Is(err, errBudget) {
			break
		}
		if err != nil {
			failed = append(failed, left[0]+": "+err.Error())
		}
		res.Rows += n
		left = left[1:]
	}
	res.Remaining, res.Done = append(res.Remaining, left...), len(left) == 0
	res.Error = strings.Join(failed, "; ")
	if err := saveCursor(left); err != nil {
		logf("warn", "import cursor: %v", err)
	}
	var runErr error
	if res.Error != "" {
		runErr = errors.New(res.Error)
	}
	finish(runErr)
	if res.Done && runErr == nil {
		if err := pruneBefore(now()); err != nil {
			logf("warn", "retention: %v", err)
		}
	}
	return res, nil
}
