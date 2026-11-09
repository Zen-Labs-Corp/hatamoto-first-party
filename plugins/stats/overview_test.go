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
	return fakeDBWith(t, map[string]string{qHeadline: head, qChannels: channels, qVersions: versions})
}

// fakeDBWith answers the statements in cells; every other overview statement
// gets an empty result.
func fakeDBWith(t *testing.T, cells map[string]string) *[]hostCall {
	t.Helper()
	canned := map[string]string{qHeadline: `[]`, qChannels: `[]`, qVersions: `[]`, qWeekly: `[]`, qPlugins: `[]`, qThemes: `[]`, qErrors: `[]`,
		qDetailed: `{"reports":0,"self_built":0}`,
		qHealth:   `{"days":0,"panics":0,"http_5xx":0,"render_errors":0,"update_failed":0,"update_rolled_back":0,"migration_failed":0}`}
	for k, v := range cells {
		canned[k] = v
	}
	var calls []hostCall
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		sql := m["sql"].(string)
		calls = append(calls, hostCall{fn, sql, m["args"].([]any)})
		cell := canned[sql]
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
	for _, q := range []string{qHeadline, qChannels, qVersions, qWeekly, qDetailed, qPlugins, qThemes, qHealth, qErrors, oldestMonthQuery("installs_daily"), oldestMonthQuery("detail_daily"), oldestMonthQuery("plugins_daily"), foldInstalls, foldDetail, foldPlugins} {
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
		if len(*calls) != 9 { // headline, channels, versions, weekly trend, 5 detailed
			t.Fatalf("%d queries", len(*calls))
		}
		for _, c := range append((*calls)[:3:3], (*calls)[4:]...) {
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
	for _, k := range []string{"versions", "trend", "plugins", "themes"} {
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

func TestOverviewDetailedFailureServesBasic(t *testing.T) {
	stubNow(t)
	fakeDBWith(t, map[string]string{qHeadline: `[{"active":5,"today":2,"new":1}]`})
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if sql := a.(map[string]any)["sql"].(string); sql == qErrors {
			return errors.New("statement timeout")
		}
		return inner(fn, a, out)
	}
	res, o := overviewGet(t, nil)
	if res.Status != 200 || o.Active != 5 || !o.DetailedUnavailable || len(o.Plugins) != 0 {
		t.Fatalf("%d %+v", res.Status, o)
	}
}

// Summed anonymous error counts can pass the int range, so qErrors must not
// cast the sum, and its row cap must keep the busiest plugins, not the first
// names alphabetically.
func TestErrorsQueryOverflowAndCap(t *testing.T) {
	if strings.Contains(qErrors, "sum(p.errors)::int") {
		t.Error("errors sum cast to int")
	}
	if !strings.Contains(qErrors, "ORDER BY count(DISTINCT p.install) DESC, p.name, d.core_version LIMIT 1000") {
		t.Error("cap does not order by installs first")
	}
	var rows []errRow
	if err := json.Unmarshal([]byte(`[{"name":"a","core":"1.0.0","errors":5000000000,"days":1,"installs":1}]`), &rows); err != nil || rows[0].Errors != 5000000000 {
		t.Fatalf("%v %+v", err, rows)
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

func TestOverviewEmptyDetailedIsEmptyArrays(t *testing.T) {
	stubNow(t)
	fakeDB(t, `[]`, `[]`, `[]`)
	res, o := overviewGet(t, nil)
	var raw map[string]json.RawMessage
	_ = json.Unmarshal([]byte(res.Body), &raw)
	if string(raw["errors"]) != `{"cores":[],"plugins":[]}` || string(raw["detailed"]) != `{"reports":0,"active":0}` || string(raw["self_built_installs"]) != "0" {
		t.Errorf("errors %s, detailed %s", raw["errors"], raw["detailed"])
	}
	if o.Health != (healthSums{}) {
		t.Errorf("health %+v", o.Health)
	}
}

func TestOverviewDetailedSeeded(t *testing.T) {
	stubNow(t)
	fakeDBWith(t, map[string]string{
		qHeadline: `[{"active":10,"today":1,"new":0}]`,
		qDetailed: `{"reports":4,"self_built":1}`,
		qPlugins:  `[{"name":"gtm","version":"1.0.0","installs":3},{"name":"gtm","version":"1.1.0","installs":1},{"name":"searchconsole","version":"1.0.0","installs":2}]`,
		qThemes:   `[{"name":"hatamoto-dev","version":"1.6.0","installs":4}]`,
		qHealth:   `{"days":10,"panics":5,"http_5xx":20,"render_errors":0,"update_failed":2,"update_rolled_back":1,"migration_failed":0}`,
		qErrors: `[
{"name":"gtm","core":"v1.0.0","errors":10,"days":10,"installs":3},
{"name":"gtm","core":"v1.1.0","errors":25,"days":10,"installs":3},
{"name":"searchconsole","core":"v1.0.0","errors":10,"days":10,"installs":3},
{"name":"searchconsole","core":"v1.1.0","errors":15,"days":10,"installs":3}]`,
	})
	_, o := overviewGet(t, nil)
	if o.Detailed != (detailedInfo{4, 10}) || o.SelfBuiltInstalls != 1 {
		t.Fatalf("detailed %+v, self built %d", o.Detailed, o.SelfBuiltInstalls)
	}
	if len(o.Plugins) != 2 || o.Plugins[0].Name != "gtm" || o.Plugins[0].Installs != 4 || o.Plugins[0].ErrorsPerDay != 1.75 ||
		len(o.Plugins[0].Versions) != 2 || o.Plugins[0].Versions[0] != (Bucket{"v1.0.0", 3}) {
		t.Fatalf("plugins %+v", o.Plugins)
	}
	if len(o.Themes) != 1 || o.Themes[0].Installs != 4 || o.Themes[0].Versions[0].Label != "v1.6.0" {
		t.Fatalf("themes %+v", o.Themes)
	}
	if o.Health.Panics != 0.5 || o.Health.HTTP5xx != 2 || o.Health.UpdateFailed != 2 || o.Health.UpdateRolledBack != 1 {
		t.Fatalf("health %+v", o.Health)
	}
	if len(o.Errors.Cores) != 2 || o.Errors.Cores[0] != "v1.1.0" || len(o.Errors.Plugins) != 2 {
		t.Fatalf("errors %+v", o.Errors)
	}
	gtm, sc := o.Errors.Plugins[0], o.Errors.Plugins[1]
	if !gtm.Cells[0].Jump || gtm.Cells[0].Core != "v1.1.0" || gtm.Cells[0].Ratio != 2.5 || gtm.Cells[1].Jump {
		t.Errorf("gtm cells %+v", gtm.Cells)
	}
	if sc.Cells[0].Jump || sc.Cells[0].Ratio != 1.5 {
		t.Errorf("searchconsole cells %+v", sc.Cells)
	}
}

func TestBuildErrorsJumpRule(t *testing.T) {
	row := func(core string, errs, installs int) errRow {
		return errRow{Name: "p", Core: core, Errors: errs, Days: 10, Installs: installs}
	}
	jump := func(rows ...errRow) bool {
		o := buildErrors(rows)
		if len(o.Plugins) != 1 {
			t.Fatalf("plugins %+v", o.Plugins)
		}
		return o.Plugins[0].Cells[0].Jump
	}
	if !jump(row("v1.0.0", 10, 5), row("v1.1.0", 20, 3)) {
		t.Error("exactly 2x on 3 installs must jump")
	}
	if jump(row("v1.0.0", 10, 5), row("v1.1.0", 20, 2)) {
		t.Error("2 installs must not jump")
	}
	if jump(row("v1.0.0", 10, 5), row("v1.1.0", 19, 5)) {
		t.Error("1.9x must not jump")
	}
	if !jump(row("v1.0.0", 0, 5), row("v1.1.0", 5, 5)) {
		t.Error("errors appearing from zero on 3+ installs must jump")
	}
	if jump(row("v1.0.0", 0, 5), row("v1.1.0", 0, 5)) {
		t.Error("zero rate must not jump")
	}
	if jump(row("v1.1.0", 50, 9)) {
		t.Error("a single core version has nothing to compare")
	}
	// The previous core is the next lower one with data, numerically ordered.
	o := buildErrors([]errRow{row("v1.9.0", 10, 4), row("v1.10.0", 30, 4), row("v1.2.0", 100, 4)})
	if c := o.Plugins[0].Cells; c[0].Core != "v1.10.0" || !c[0].Jump || c[0].Ratio != 3 || c[1].Jump {
		t.Errorf("cells %+v", c)
	}
	// Only the highest four cores are shown.
	o = buildErrors([]errRow{row("v1.0.0", 1, 5), row("v1.1.0", 1, 5), row("v1.2.0", 1, 5), row("v1.3.0", 1, 5), row("v1.4.0", 1, 5)})
	if len(o.Cores) != 4 || o.Cores[3] != "v1.1.0" || len(o.Plugins[0].Cells) != 4 {
		t.Errorf("cores %v, cells %d", o.Cores, len(o.Plugins[0].Cells))
	}
}
