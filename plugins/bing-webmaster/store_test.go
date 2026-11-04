package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"testing"
	"time"
)

var bannedSQL = regexp.MustCompile(`(?i)\b(public|copy)\b|pg_`)

// execs returns the db.exec calls, checking the host's SQL rules on each.
func execs(t *testing.T, h *fakeHost) []hostCall {
	t.Helper()
	var out []hostCall
	for _, c := range h.calls {
		if c.fn != "db.exec" && c.fn != "db.query" {
			continue
		}
		q := c.sql()
		if bannedSQL.MatchString(q) || strings.Contains(strings.TrimRight(q, "; "), ";") {
			t.Errorf("sql breaks the host rules: %s", q)
		}
		if c.fn == "db.exec" {
			out = append(out, c)
		}
	}
	return out
}

func TestStatsInsertsAreUpserts(t *testing.T) {
	h := newHost(t)
	if err := storeTraffic([]trafficRow{{Day: "2026-10-01", Clicks: 1, Impressions: 2}}); err != nil {
		t.Fatal(err)
	}
	rows := []statRow{{Key: "a", Week: "2026-10-01", Clicks: 1}}
	for _, table := range []string{"query_stats", "page_stats"} {
		if err := storeStats(table, rows); err != nil {
			t.Fatal(err)
		}
	}
	got := execs(t, h)
	if len(got) != 3 {
		t.Fatalf("%d statements", len(got))
	}
	pk := []string{"(day)", "(week, query)", "(week, page)"}
	for i, c := range got {
		if !strings.Contains(c.sql(), "ON CONFLICT "+pk[i]+" DO UPDATE SET") {
			t.Errorf("no upsert on %s: %s", pk[i], c.sql())
		}
	}
	if !strings.Contains(got[1].sql(), "VALUES ($1::date, $2, $3, $4, $5, $6)") {
		t.Fatal(got[1].sql())
	}
}

func TestStoreStatsRejectsOtherTables(t *testing.T) {
	h := newHost(t)
	for _, table := range []string{"feeds", "query_stats; DROP TABLE feeds", ""} {
		if err := storeStats(table, []statRow{{Key: "a", Week: "2026-10-01"}}); err == nil {
			t.Errorf("%q accepted", table)
		}
	}
	if len(h.calls) != 0 {
		t.Fatal("no statement may run")
	}
}

func TestStoreBatchesAndDedupes(t *testing.T) {
	h := newHost(t)
	var rows []statRow
	for i := 0; i < 450; i++ {
		rows = append(rows, statRow{Key: fmt.Sprint("q", i), Week: "2026-10-01"})
	}
	rows = append(rows, statRow{Key: "q0", Week: "2026-10-01", Clicks: 9}) // same key again: last wins
	if err := storeStats("query_stats", rows); err != nil {
		t.Fatal(err)
	}
	got := execs(t, h)
	if len(got) != 3 {
		t.Fatalf("%d statements for 450 keys", len(got))
	}
	if n := len(got[0].args["args"].([]any)); n != 200*6 {
		t.Fatalf("first batch has %d args", n)
	}
	if a := got[0].args["args"].([]any); a[1] != "q0" || a[2] != 9 {
		t.Fatalf("duplicate not collapsed: %v", a[:6])
	}
	if err := storeStats("query_stats", nil); err != nil || len(execs(t, h)) != 3 {
		t.Fatal("no rows must issue no statement")
	}
}

func TestSnapshotsAreUpsertedThenSwept(t *testing.T) {
	h := newHost(t)
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	if err := replaceCrawlIssues([]crawlRow{{URL: "https://example.com/a", HTTPCode: 404, Issues: 4}, {URL: "https://example.com/a"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := replaceFeeds([]feedRow{{URL: "https://example.com/sitemap.xml", Status: "Success", SubmittedAt: &now}}, now); err != nil {
		t.Fatal(err)
	}
	got := execs(t, h)
	want := []string{"INSERT INTO crawl_issues", "DELETE FROM crawl_issues WHERE seen_at < $1", "INSERT INTO feeds", "DELETE FROM feeds WHERE seen_at < $1"}
	if len(got) != len(want) {
		t.Fatalf("%d statements", len(got))
	}
	for i, w := range want {
		if !strings.HasPrefix(got[i].sql(), w) {
			t.Errorf("%d: %s", i, got[i].sql())
		}
	}
	if !strings.Contains(got[0].sql(), "ON CONFLICT (url) DO UPDATE") || !strings.Contains(got[2].sql(), "ON CONFLICT (url) DO UPDATE") {
		t.Fatal("snapshot rows must be upserts")
	}
	if n := len(got[0].args["args"].([]any)); n != 5 {
		t.Fatalf("duplicate url not collapsed: %d args", n)
	}
	// An empty answer from a successful fetch sweeps everything and inserts nothing.
	h.calls = nil
	if err := replaceCrawlIssues(nil, now); err != nil {
		t.Fatal(err)
	}
	if got := execs(t, h); len(got) != 1 || !strings.HasPrefix(got[0].sql(), "DELETE FROM crawl_issues WHERE") {
		t.Fatalf("%+v", got)
	}
}

func TestFailedSnapshotInsertKeepsTheOldRows(t *testing.T) {
	h := newHost(t)
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn == "db.exec" && strings.HasPrefix(a.(map[string]any)["sql"].(string), "INSERT") {
			return errors.New("insert failed")
		}
		return inner(fn, a, out)
	}
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	if err := replaceCrawlIssues([]crawlRow{{URL: "https://example.com/a"}}, now); err == nil {
		t.Fatal("want the insert error")
	}
	if err := replaceFeeds([]feedRow{{URL: "https://example.com/s.xml"}}, now); err == nil {
		t.Fatal("want the insert error")
	}
	for _, c := range execs(t, h) {
		if strings.HasPrefix(c.sql(), "DELETE") {
			t.Fatalf("old snapshot deleted after a failed insert: %s", c.sql())
		}
	}
}

func TestOverLongKeysAreSkipped(t *testing.T) {
	h := newHost(t)
	now := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	long := "https://example.com/" + strings.Repeat("x", maxKeyBytes)
	if err := replaceCrawlIssues([]crawlRow{{URL: long}, {URL: "https://example.com/ok"}}, now); err != nil {
		t.Fatal(err)
	}
	if err := storeStats("page_stats", []statRow{{Key: long, Week: "2026-10-01"}, {Key: "https://example.com/ok", Week: "2026-10-01"}}); err != nil {
		t.Fatal(err)
	}
	got := execs(t, h)
	if n := len(got[0].args["args"].([]any)); n != 5 {
		t.Fatalf("crawl insert has %d args, want only the short row", n)
	}
	if n := len(got[2].args["args"].([]any)); n != 6 {
		t.Fatalf("stats insert has %d args, want only the short row", n)
	}
}

func TestPruneCutoffs(t *testing.T) {
	for _, tc := range []struct {
		now, day string
		logs     time.Time
	}{
		{"2026-11-04T09:30:00Z", "2025-07-04", time.Date(2026, 10, 5, 9, 30, 0, 0, time.UTC)},
		{"2026-03-31T23:59:59Z", "2024-11-30", time.Date(2026, 3, 1, 23, 59, 59, 0, time.UTC)}, // month-end clamps
		{"2026-11-04T01:00:00+14:00", "2025-07-03", time.Date(2026, 10, 4, 11, 0, 0, 0, time.UTC)},
	} {
		h := newHost(t)
		now, _ := time.Parse(time.RFC3339, tc.now)
		if err := pruneBefore(now); err != nil {
			t.Fatal(err)
		}
		got := execs(t, h)
		if len(got) != 5 {
			t.Fatalf("%d statements", len(got))
		}
		tables := []string{"traffic_daily", "query_stats", "page_stats", "import_runs", "sitemap_submissions"}
		for i, c := range got {
			if !strings.HasPrefix(c.sql(), "DELETE FROM "+tables[i]+" WHERE ") {
				t.Errorf("%d: %s", i, c.sql())
			}
			arg := c.args["args"].([]any)[0]
			if i < 3 && arg != tc.day {
				t.Errorf("%s cutoff %v, want %s", tables[i], arg, tc.day)
			}
			if i >= 3 && !arg.(time.Time).Equal(tc.logs) {
				t.Errorf("%s cutoff %v, want %v", tables[i], arg, tc.logs)
			}
		}
	}
}

func TestPruneTriesEveryTable(t *testing.T) {
	h := newHost(t)
	h.dbErr = errors.New("db down")
	if err := pruneBefore(time.Now()); err == nil {
		t.Fatal("want the error")
	}
	if n := len(h.calls); n != 5 {
		t.Fatalf("%d statements tried", n)
	}
}

func TestRecordRunAndSubmission(t *testing.T) {
	h := newHost(t)
	at := time.Date(2026, 11, 4, 9, 0, 0, 0, time.UTC)
	if err := recordRun(at, at.Add(time.Second), errors.New(strings.Repeat("e", 900))); err != nil {
		t.Fatal(err)
	}
	if err := recordSubmission("https://example.com/sitemap.xml", at, nil); err != nil {
		t.Fatal(err)
	}
	got := execs(t, h)
	a := got[0].args["args"].([]any)
	if a[2] != false || len(a[3].(string)) != maxErrorText {
		t.Fatalf("run args %v", a)
	}
	if b := got[1].args["args"].([]any); b[2] != "" {
		t.Fatalf("submission args %v", b)
	}
}

func TestLoadStoredFillsEmptyLists(t *testing.T) {
	h := newHost(t)
	callHost = func(fn string, a, out any) error {
		if fn == "db.query" {
			cell := `{"traffic":null,"top_queries":[{"key":"hatamoto cms","clicks":40,"impressions":900,"position":3.1}],"last_import":{"at":"2026-11-04T14:00:00+00:00","ok":true,"error":""}}`
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		}
		return h.call(fn, a, out)
	}
	s, err := loadStored()
	if err != nil {
		t.Fatal(err)
	}
	if s.Traffic == nil || s.TopPages == nil || s.CrawlIssues == nil || s.Feeds == nil || s.LastSubmission != nil {
		t.Fatalf("%+v", s)
	}
	if s.TopQueries[0].Key != "hatamoto cms" || s.LastRun == nil || !s.LastRun.OK {
		t.Fatalf("%+v", s)
	}
	if bannedSQL.MatchString(qOverview) {
		t.Fatal("overview sql breaks the host rules")
	}
}
