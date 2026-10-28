package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// fakeDB answers each overview query with a canned jsonb cell and records the
// calls.
func fakeDB(t *testing.T, head, channels, versions string) *[]hostCall {
	t.Helper()
	var calls []hostCall
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		sql := m["sql"].(string)
		calls = append(calls, hostCall{fn, sql, m["args"].([]any)})
		cell := map[string]string{qHeadline: head, qChannels: channels, qVersions: versions, qWeekly: `[]`}[sql]
		if cell == "" {
			t.Fatalf("unexpected statement %q", sql)
		}
		return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
	}
	return &calls
}

func overviewGet(t *testing.T, query map[string][]string) (sdk.HTTPResponse, overview) {
	t.Helper()
	res := send(t, sdk.HTTPRequest{Method: "GET", Pattern: "/overview", Query: query})
	var o overview
	if res.Status == 200 {
		if err := json.Unmarshal([]byte(res.Body), &o); err != nil {
			t.Fatal(err)
		}
	}
	return res, o
}

func stubNow(t *testing.T) {
	t.Helper()
	old := now
	t.Cleanup(func() { now = old })
	now = func() time.Time { return time.Date(2026, 10, 28, 9, 30, 0, 0, time.UTC) }
}

func TestOverviewQueriesPassGuard(t *testing.T) {
	for _, q := range []string{qHeadline, qChannels, qVersions, qWeekly, qOldestMonth, foldMonth} {
		if !sqlGuardOK(q) {
			t.Errorf("guard rejects %q", q)
		}
		if strings.Contains(strings.ToLower(q), "now()") || strings.Contains(strings.ToLower(q), "current_date") {
			t.Errorf("database clock used in %q", q)
		}
	}
}

func TestOverviewWindowAndShape(t *testing.T) {
	stubNow(t)
	for days, start := range map[string]string{"7": "2026-10-21", "30": "2026-09-28", "90": "2026-07-30"} {
		calls := fakeDB(t,
			`[{"active":10,"today":3,"new":2}]`,
			`[{"label":"edge","count":1},{"label":"stable","count":9}]`,
			`[{"label":"v1.2.0","count":6},{"label":"v1.1.0","count":4}]`)
		res, o := overviewGet(t, map[string][]string{"days": {days}})
		if res.Status != 200 {
			t.Fatalf("days=%s: %d", days, res.Status)
		}
		if len(*calls) != 4 { // headline, channels, versions, weekly trend
			t.Fatalf("%d queries", len(*calls))
		}
		for _, c := range (*calls)[:3] {
			if c.fn != "db.query" || c.args[0] != start || c.args[1] != "2026-10-28" {
				t.Fatalf("days=%s: args %v", days, c.args)
			}
		}
		if o.Active != 10 || o.Today != 3 || o.New != 2 {
			t.Fatalf("headline %+v", o)
		}
		if len(o.Channels) != 3 || o.Channels[0] != (Bucket{"stable", 9}) || o.Channels[2] != (Bucket{"canary", 0}) {
			t.Fatalf("channels %v", o.Channels)
		}
		if len(o.Versions) != 2 || o.Versions[0].Label != "v1.2.0" {
			t.Fatalf("versions %v", o.Versions)
		}
	}
}

func TestOverviewDefaultsToThirtyDays(t *testing.T) {
	stubNow(t)
	fakeDB(t, `[{"active":0,"today":0,"new":0}]`, `[]`, `[]`)
	res, o := overviewGet(t, nil)
	if res.Status != 200 || o.Days != 30 {
		t.Fatalf("%d, days %d", res.Status, o.Days)
	}
}

func TestOverviewRejectsOtherDays(t *testing.T) {
	for _, d := range []string{"0", "1", "31", "365", "-30", "abc", "+30", "030", " 30", ""} {
		calls := fakeDB(t, `[]`, `[]`, `[]`)
		res, _ := overviewGet(t, map[string][]string{"days": {d}})
		want := 422
		if d == "" {
			want = 200
		}
		if res.Status != want {
			t.Errorf("days=%q: %d", d, res.Status)
		}
		if want == 422 && (len(*calls) != 0 || !strings.Contains(res.Body, "days must be 7, 30 or 90")) {
			t.Errorf("days=%q: %d calls, body %q", d, len(*calls), res.Body)
		}
	}
}

func TestOverviewEmptyDatabase(t *testing.T) {
	stubNow(t)
	fakeDB(t, `[]`, `[]`, `[]`)
	res, _ := overviewGet(t, nil)
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(res.Body), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"versions", "trend"} {
		if string(raw[k]) != "[]" {
			t.Errorf("%s = %s, want []", k, raw[k])
		}
	}
	if string(raw["channels"]) != `[{"label":"stable","count":0},{"label":"edge","count":0},{"label":"canary","count":0}]` {
		t.Errorf("channels = %s", raw["channels"])
	}
	for _, k := range []string{"active", "today", "new"} {
		if string(raw[k]) != "0" {
			t.Errorf("%s = %s", k, raw[k])
		}
	}
}

func TestOverviewDatabaseFailureIs503(t *testing.T) {
	callHost = func(_ string, _, _ any) error { return errors.New("capability_denied: db") }
	res, _ := overviewGet(t, nil)
	if res.Status != 503 || res.Body != `{"error":"the request failed"}` {
		t.Fatalf("%d %q", res.Status, res.Body)
	}
}

// New is probed once per distinct install in the window, not once per daily
// row: the per-row form took about a second for 10k installs pinging daily.
func TestHeadlineProbesDistinctInstalls(t *testing.T) {
	if !strings.HasPrefix(qHeadline, "WITH win AS (SELECT DISTINCT install FROM installs_daily") {
		t.Fatal("qHeadline must start from the distinct installs of the window")
	}
	if strings.Contains(qHeadline, "count(DISTINCT w.install)") || !strings.Contains(qHeadline, "FROM win w") {
		t.Fatal("qHeadline must count New over the distinct installs")
	}
}

// The newest row per install is one index probe per install, not a sort of the
// window (about 0.6 s against 0.15 s for 10k installs over 90 days).
func TestLatestInWindowProbesPerInstall(t *testing.T) {
	for _, q := range []string{qChannels, qVersions} {
		if !strings.Contains(q, "CROSS JOIN LATERAL") || !strings.Contains(q, "ORDER BY d.day DESC LIMIT 1") || strings.Contains(q, "DISTINCT ON") {
			t.Errorf("query does not probe the newest row per install: %s", q)
		}
	}
}
