package main

import (
	"fmt"
	"reflect"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const testSite = "https://example.com/"

var fixedNow = time.Date(2026, 11, 4, 14, 0, 0, 0, time.UTC)

// goodBing answers every endpoint with a little data.
func goodBing(h *fakeHost) {
	h.settings.SiteURL = testSite
	h.bing["GetRankAndTrafficStats"] = ok(`{"d":[{"Date":"/Date(1700000000000)/","Clicks":5,"Impressions":50}]}`)
	h.bing["GetQueryStats"] = ok(`{"d":[{"Query":"hatamoto cms","Date":"/Date(1700000000000)/","Clicks":4,"Impressions":40,"AvgClickPosition":2.5,"AvgImpressionPosition":3.1}]}`)
	h.bing["GetPageStats"] = ok(`{"d":[{"Query":"https://example.com/pricing","Date":"/Date(1700000000000)/","Clicks":3,"Impressions":30}]}`)
	h.bing["GetCrawlIssues"] = ok(`{"d":[{"Url":"https://example.com/old","HttpCode":404,"Issues":4,"InLinks":3}]}`)
	h.bing["GetFeeds"] = ok(`{"d":[{"Url":"https://example.com/sitemap.xml","Status":"Success","UrlCount":10}]}`)
}

func clockAt(t time.Time) func() time.Time { return func() time.Time { return t } }

// stmts returns the SQL of the statements that write rows, in order.
func stmts(t *testing.T, h *fakeHost) []string {
	var out []string
	for _, c := range execs(t, h) {
		out = append(out, c.sql())
	}
	return out
}

func writes(h *fakeHost, prefix string) []hostCall {
	var out []hostCall
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql(), prefix) {
			out = append(out, c)
		}
	}
	return out
}

func lastRun(t *testing.T, h *fakeHost) (okRun bool, errText string) {
	t.Helper()
	runs := writes(h, "INSERT INTO import_runs")
	if len(runs) == 0 {
		t.Fatal("no import run recorded")
	}
	a := runs[len(runs)-1].args["args"].([]any)
	return a[2].(bool), a[3].(string)
}

func TestRejectedKeyStoresNothingAndRecordsTheRun(t *testing.T) {
	h := newHost(t)
	h.settings.SiteURL = testSite
	for _, name := range []string{"GetRankAndTrafficStats", "GetQueryStats", "GetPageStats", "GetCrawlIssues", "GetFeeds"} {
		h.bing[name] = fetched{Status: 401, Body: `{"Message":"InvalidApiKey"}`}
	}
	res, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow))
	if err != errKeyDenied || res.Rows != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	for _, s := range stmts(t, h) {
		if !strings.HasPrefix(s, "INSERT INTO import_runs") {
			t.Errorf("unexpected write: %s", s)
		}
	}
	if okRun, text := lastRun(t, h); okRun || text != "Bing rejected the API key." {
		t.Fatalf("run ok=%v %q", okRun, text)
	}
	if len(h.fetches()) != 1 {
		t.Fatalf("%d fetches after the key was rejected", len(h.fetches()))
	}
	// The cron fails open.
	if err := onImport(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
}

func TestMissingKeyDoesNothing(t *testing.T) {
	h := newHost(t)
	h.settings.APIKey = ""
	if err := onImport(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.calls {
		if c.fn != "settings.get" {
			t.Fatalf("unexpected host call %s", c.fn)
		}
	}
}

func TestImportStoresEverythingAndPrunes(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	res, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow))
	if err != nil || !res.Done || res.Rows != 5 || res.Error != "" || len(res.Remaining) != 0 {
		t.Fatalf("%v %+v", err, res)
	}
	want := []string{"INSERT INTO traffic_daily", "INSERT INTO query_stats", "INSERT INTO page_stats", "DELETE FROM crawl_issues", "INSERT INTO crawl_issues",
		"DELETE FROM feeds", "INSERT INTO feeds", "INSERT INTO import_runs",
		"DELETE FROM traffic_daily", "DELETE FROM query_stats", "DELETE FROM page_stats", "DELETE FROM import_runs", "DELETE FROM sitemap_submissions"}
	got := stmts(t, h)
	if len(got) != len(want) {
		t.Fatalf("%d statements: %v", len(got), got)
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i], w) {
			t.Errorf("%d: %s, want %s", i, got[i], w)
		}
	}
	if okRun, _ := lastRun(t, h); !okRun {
		t.Fatal("run must be ok")
	}
	if _, left := h.kv["cursor"]; left {
		t.Fatal("cursor must be cleared")
	}
}

// Two imports over the same Bing data write the same keys: nothing can grow.
func TestRepeatedImportUpsertsTheSameKeys(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	var runs [][]hostCall
	for i := 0; i < 2; i++ {
		h.calls = nil
		if _, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow)); err != nil {
			t.Fatal(err)
		}
		var up []hostCall
		for _, c := range execs(t, h) {
			if strings.Contains(c.sql(), "ON CONFLICT") {
				if !strings.Contains(c.sql(), "DO UPDATE") {
					t.Errorf("no upsert: %s", c.sql())
				}
				up = append(up, c)
			}
		}
		runs = append(runs, up)
	}
	if len(runs[0]) != 3 || !reflect.DeepEqual(runs[0], runs[1]) {
		t.Fatalf("%d upserts; second run differs", len(runs[0]))
	}
}

func TestCronPrunesAtTheRetentionCutoffs(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	clock = clockAt(fixedNow)
	t.Cleanup(func() { clock = time.Now })
	if err := onImport(sdk.CronEvent{}); err != nil {
		t.Fatal(err)
	}
	cut := map[string]any{}
	for _, c := range h.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql(), "DELETE FROM") && strings.Contains(c.sql(), " WHERE ") {
			cut[strings.Fields(c.sql())[2]] = c.args["args"].([]any)[0]
		}
	}
	for _, table := range []string{"traffic_daily", "query_stats", "page_stats"} {
		if cut[table] != "2025-07-04" {
			t.Errorf("%s cutoff %v", table, cut[table])
		}
	}
	for _, table := range []string{"import_runs", "sitemap_submissions"} {
		if at, _ := cut[table].(time.Time); !at.Equal(time.Date(2026, 10, 5, 14, 0, 0, 0, time.UTC)) {
			t.Errorf("%s cutoff %v", table, cut[table])
		}
	}
}

func TestTruncatedEndpointDoesNotStopTheOthers(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	h.bing["GetQueryStats"] = fetched{Status: 200, Body: `{"d":[`, Truncated: true}
	res, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow))
	if err != nil || !res.Done || !strings.Contains(res.Error, "queries: Bing's answer for GetQueryStats was too large") {
		t.Fatalf("%v %+v", err, res)
	}
	if len(writes(h, "INSERT INTO query_stats")) != 0 {
		t.Fatal("a truncated answer must store nothing")
	}
	for _, p := range []string{"INSERT INTO traffic_daily", "INSERT INTO page_stats", "INSERT INTO crawl_issues", "INSERT INTO feeds"} {
		if len(writes(h, p)) != 1 {
			t.Errorf("%s not written", p)
		}
	}
	if okRun, text := lastRun(t, h); okRun || !strings.Contains(text, "GetQueryStats") {
		t.Fatalf("run ok=%v %q", okRun, text)
	}
	if len(writes(h, "DELETE FROM traffic_daily")) != 0 {
		t.Fatal("no retention after a failed run")
	}
}

func TestFailedFetchKeepsTheOldSnapshot(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	h.bing["GetCrawlIssues"] = fetched{Status: 500, Body: `{"Message":"later"}`}
	h.bing["GetFeeds"] = fetched{Status: 200, Body: `{"d":[`, Truncated: true}
	if _, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow)); err != nil {
		t.Fatal(err)
	}
	if n := len(writes(h, "DELETE FROM crawl_issues")) + len(writes(h, "DELETE FROM feeds")); n != 0 {
		t.Fatalf("%d snapshot deletes after failed fetches", n)
	}
	// An empty list from a successful fetch does clear the snapshot.
	h.calls = nil
	h.bing["GetCrawlIssues"] = ok(`{"d":[]}`)
	if _, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow)); err != nil {
		t.Fatal(err)
	}
	if len(writes(h, "DELETE FROM crawl_issues")) != 1 || len(writes(h, "INSERT INTO crawl_issues")) != 0 {
		t.Fatal("empty snapshot not replaced")
	}
}

func TestSiteProblemsAreRecordedAndStopTheRun(t *testing.T) {
	h := newHost(t)
	h.bing["GetUserSites"] = ok(`{"d":[]}`)
	_, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow))
	if err != errSiteChoice {
		t.Fatal(err)
	}
	if okRun, _ := lastRun(t, h); okRun {
		t.Fatal("run must be recorded as failed")
	}
	if len(h.fetches()) != 1 {
		t.Fatal("no data endpoint may be asked without a site")
	}
}

func TestBudgetStopSavesTheCursorAndTheNextRunContinues(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	slow := h.call
	callHost = func(fn string, a, out any) error {
		if fn == "http.fetch" {
			time.Sleep(200 * time.Millisecond)
		}
		return slow(fn, a, out)
	}
	res, err := runImport(h.settings, time.Now().Add(endpointReserve+300*time.Millisecond), clockAt(fixedNow))
	if err != nil || res.Done || !slices.Equal(res.Remaining, []string{"pages", "crawl", "feeds"}) {
		t.Fatalf("%v %+v", err, res)
	}
	saved := string(h.kv["cursor"])
	if saved != `["pages","crawl","feeds"]` {
		t.Fatalf("cursor %s", saved)
	}
	if len(writes(h, "DELETE FROM traffic_daily")) != 0 {
		t.Fatal("no retention until the import is complete")
	}
	callHost = slow
	h.calls = nil
	res, err = runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow))
	if err != nil || !res.Done {
		t.Fatalf("%v %+v", err, res)
	}
	if n := len(h.fetches()); n != 3 {
		t.Fatalf("second run fetched %d endpoints, want the 3 left", n)
	}
	if _, left := h.kv["cursor"]; left {
		t.Fatal("cursor must be cleared")
	}
}

func TestCorruptCursorStartsAFullImport(t *testing.T) {
	for _, junk := range []string{`"junk"`, `["bogus"]`, `{}`, `[]`, `["feeds","nope"]`} {
		h := newHost(t)
		goodBing(h)
		h.kv["cursor"] = []byte(junk)
		if _, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow)); err != nil {
			t.Fatal(err)
		}
		if n := len(h.fetches()); n != 5 {
			t.Errorf("cursor %s: %d fetches, want 5", junk, n)
		}
	}
}

func TestNoLogOrRecordCarriesTheKey(t *testing.T) {
	h := newHost(t)
	goodBing(h)
	h.bing["GetPageStats"] = fetched{Status: 500, Body: `{"Message":"bad ` + testKey + `"}`}
	if _, err := runImport(h.settings, time.Now().Add(importBudget), clockAt(fixedNow)); err != nil {
		t.Fatal(err)
	}
	for _, c := range h.calls {
		if c.fn == "http.fetch" {
			continue
		}
		if s := strings.ToLower(strings.TrimSpace(strings.Join([]string{c.sql(), fmt.Sprint(c.args)}, " "))); strings.Contains(s, strings.ToLower(testKey)) || strings.Contains(s, "apikey=") {
			t.Fatalf("%s carries the key: %s", c.fn, s)
		}
	}
}
