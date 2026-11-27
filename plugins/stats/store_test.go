package main

import (
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

var (
	guardFirstWord = regexp.MustCompile(`(?i)^(select|insert|update|delete|with|values)\b`)
	guardBanned    = regexp.MustCompile(`(?i)\bpublic\b|pg_|information_schema`)
)

// sqlGuardOK reports whether sql looks like one statement the plugin role may
// run: an allowed first word, no statement separator, no schema or catalog
// access.
func sqlGuardOK(sql string) bool {
	s := strings.TrimSpace(sql)
	return guardFirstWord.MatchString(s) &&
		!strings.Contains(strings.TrimSuffix(s, ";"), ";") &&
		!guardBanned.MatchString(s)
}

func TestSQLGuard(t *testing.T) {
	for sql, want := range map[string]bool{
		upsertDaily:                          true,
		upsertDetailed:                       true,
		"DROP TABLE x":                       false,
		"SELECT 1; DELETE FROM x":            false,
		"SELECT * FROM public.x":             false,
		"SELECT * FROM pg_user":              false,
		"SELECT * FROM information_schema.t": false,
	} {
		if got := sqlGuardOK(sql); got != want {
			t.Errorf("sqlGuardOK(%q) = %v", sql, got)
		}
	}
}

func TestMigrationHasNoFunctionsTriggersOrGrants(t *testing.T) {
	files, err := filepath.Glob("migrations/*.sql")
	if err != nil || len(files) < 2 {
		t.Fatalf("migrations: %v, %v", files, err)
	}
	for _, f := range files {
		raw, err := os.ReadFile(f)
		if err != nil {
			t.Fatal(err)
		}
		if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema)\b`).FindString(string(raw)); bad != "" {
			t.Fatalf("%s contains %q", f, bad)
		}
	}
}

type hostCall struct {
	fn   string
	sql  string
	args []any
}

// recordHost replaces callHost with a recorder and returns its calls.
func recordHost(t *testing.T) *[]hostCall {
	t.Helper()
	var calls []hostCall
	callHost = func(fn string, a, _ any) error {
		m := a.(map[string]any)
		calls = append(calls, hostCall{fn, m["sql"].(string), m["args"].([]any)})
		return nil
	}
	return &calls
}

func TestRecordPingIssuesOneUpsert(t *testing.T) {
	calls := recordHost(t)
	p := ping{Install: "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65", Version: "v1.2.3", Channel: "stable"}
	if err := recordPing(p, "2026-10-28"); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 1 {
		t.Fatalf("%d host calls", len(*calls))
	}
	c := (*calls)[0]
	if c.fn != "db.exec" || c.sql != upsertDaily || !sqlGuardOK(c.sql) {
		t.Fatalf("call %+v", c)
	}
	want := []any{p.Install, "2026-10-28", "v1.2.3", "stable"}
	for i := range want {
		if c.args[i] != want[i] {
			t.Fatalf("args %v, want %v", c.args, want)
		}
	}
}

func TestUTCDayBoundary(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	zone := time.FixedZone("far-east", 14*3600)
	for at, want := range map[time.Time]string{
		time.Date(2026, 10, 28, 23, 59, 59, 0, time.UTC): "2026-10-28",
		time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC):    "2026-10-29",
		time.Date(2026, 10, 29, 9, 0, 0, 0, zone):        "2026-10-28",
	} {
		now = func() time.Time { return at }
		if got := utcDay(); got != want {
			t.Errorf("%v: %s, want %s", at, got, want)
		}
	}
}

func TestDetailedPingIsOneStatement(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 11, 9, 23, 59, 0, 0, time.UTC) }
	calls := recordHost(t)
	if res := send(t, jsonReq(detailedBody)); res.Status != 204 {
		t.Fatalf("got %d", res.Status)
	}
	if len(*calls) != 1 {
		t.Fatalf("%d host calls", len(*calls))
	}
	c := (*calls)[0]
	if c.fn != "db.exec" || c.sql != upsertDetailed || !sqlGuardOK(c.sql) {
		t.Fatalf("call %+v", c)
	}
	if len(c.args) != 30 || c.args[0] != goodID || c.args[1] != "2026-11-09" || c.args[2] != "v1.1.0" || c.args[3] != "stable" {
		t.Fatalf("args %v", c.args)
	}
	list, _ := c.args[20].(string)
	if !strings.Contains(list, `{"name":"searchconsole","version":"1.0.0","enabled":true,"errors":1}`) {
		t.Fatalf("plugin list %s", list)
	}
}

func TestDetailedHostArg(t *testing.T) {
	calls := recordHost(t)
	if err := recordDetailed(detailedPing{}, "2026-11-09"); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].args[21]; got != nil {
		t.Fatalf("absent host sent %#v, want nil", got)
	}
	if err := recordDetailed(detailedPing{Host: "example.com", HostSet: true}, "2026-11-09"); err != nil {
		t.Fatal(err)
	}
	c := (*calls)[1]
	if len(c.args) != 30 || c.args[21] != "example.com" || !sqlGuardOK(c.sql) {
		t.Fatalf("args %v", c.args)
	}
	if n := strings.Count(upsertDetailed, ";"); n != 0 {
		t.Fatalf("%d semicolons", n)
	}
}

func TestDetailedFlagArgs(t *testing.T) {
	calls := recordHost(t)
	if err := recordDetailed(detailedPing{}, "2026-11-09"); err != nil {
		t.Fatal(err)
	}
	for i := 22; i < 30; i++ {
		if got := (*calls)[0].args[i]; got != nil {
			t.Errorf("absent flag arg %d is %#v, want nil", i, got)
		}
	}
	yes, no, bucket := true, false, "2-3"
	p := detailedPing{Flags: flags{CommentsOn: &yes, StaticMode: &no, SSOOn: &yes, LocalesBucket: &bucket,
		FormsInUse: &no, OffsiteBackupsOn: &yes, CSPEnforced: &no, WPImported: &yes}}
	if err := recordDetailed(p, "2026-11-09"); err != nil {
		t.Fatal(err)
	}
	want := []any{true, false, true, "2-3", false, true, false, true}
	for i, w := range want {
		if got := (*calls)[1].args[22+i]; got != w {
			t.Errorf("flag arg %d is %#v, want %#v", 22+i, got, w)
		}
	}
}

func TestDetailedWithoutPluginsSendsEmptyList(t *testing.T) {
	calls := recordHost(t)
	if err := recordDetailed(detailedPing{}, "2026-11-09"); err != nil {
		t.Fatal(err)
	}
	if got := (*calls)[0].args[20]; got != "[]" {
		t.Fatalf("plugin list %v", got)
	}
}

func TestDetailedSQLShape(t *testing.T) {
	// A same-day report replaces that day's plugin rows, and no database clock
	// is involved: the day is an argument.
	for _, want := range []string{"NOT IN (SELECT name FROM jsonb_to_recordset($21::jsonb)", "ON CONFLICT (install, day, name) DO UPDATE", "host = COALESCE(EXCLUDED.host, detail_daily.host)", "host, comments_on, static_mode, sso_on, locales_bucket, forms_in_use, offsite_backups_on, csp_enforced, wp_imported) ", "$22, $23, $24, $25, $26, $27, $28, $29, $30)", "COALESCE(EXCLUDED.comments_on, detail_daily.comments_on)", "COALESCE(EXCLUDED.static_mode, detail_daily.static_mode)", "COALESCE(EXCLUDED.sso_on, detail_daily.sso_on)", "COALESCE(EXCLUDED.locales_bucket, detail_daily.locales_bucket)", "COALESCE(EXCLUDED.forms_in_use, detail_daily.forms_in_use)", "COALESCE(EXCLUDED.offsite_backups_on, detail_daily.offsite_backups_on)", "COALESCE(EXCLUDED.csp_enforced, detail_daily.csp_enforced)", "COALESCE(EXCLUDED.wp_imported, detail_daily.wp_imported)"} {
		if !strings.Contains(upsertDetailed, want) {
			t.Errorf("missing %q", want)
		}
	}
	if low := strings.ToLower(upsertDetailed); strings.Contains(low, "now()") || strings.Contains(low, "current_date") {
		t.Error("database clock in upsertDetailed")
	}
}

func TestMigration004Shape(t *testing.T) {
	raw, err := os.ReadFile("migrations/004_flags_ip.sql")
	if err != nil {
		t.Fatal(err)
	}
	sql := string(raw)
	for _, want := range []string{"DROP CONSTRAINT detail_daily_host_check,", "ADD CONSTRAINT detail_daily_host_check"} {
		if !strings.Contains(sql, want) {
			t.Errorf("missing %q", want)
		}
	}
	for _, col := range []string{"comments_on", "static_mode", "sso_on", "locales_bucket", "forms_in_use", "offsite_backups_on", "csp_enforced", "wp_imported"} {
		if n := strings.Count(sql, "ADD COLUMN "+col+" "); n != 2 {
			t.Errorf("ADD COLUMN %s appears %d times, want 2", col, n)
		}
	}
	if n := strings.Count(sql, "IN ('1', '2-3', '4+')"); n != 2 {
		t.Errorf("locales CHECK appears %d times, want 2", n)
	}
	if strings.Contains(strings.ToUpper(sql), "IF EXISTS") {
		t.Error("must not use IF EXISTS")
	}
}
