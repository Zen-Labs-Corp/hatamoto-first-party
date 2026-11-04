package main

import (
	"encoding/json"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

var (
	guardFirstWord = regexp.MustCompile(`(?i)^(select|insert|update|delete|with|values)\b`)
	guardBanned    = regexp.MustCompile(`(?i)\bpublic\b|pg_|information_schema|\bcopy\b|current_setting|set_config`)
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
		sqlInsertPayment:                     true,
		sqlListPayments:                      true,
		sqlMonthTotals:                       true,
		sqlEventSeen:                         true,
		sqlPrune:                             true,
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
	raw, err := os.ReadFile("migrations/001_init.sql")
	if err != nil {
		t.Fatal(err)
	}
	if bad := regexp.MustCompile(`(?i)\b(function|trigger|grant|schema)\b`).FindString(string(raw)); bad != "" {
		t.Fatalf("migration contains %q", bad)
	}
}

type hostCall struct {
	fn   string
	sql  string
	args []any
}

// dbHost fakes the database calls. exec returns rows_affected per call; query
// returns the one jsonb cell. Every statement must pass the SQL guard.
func dbHost(t *testing.T, affected []int64, cell string) *[]hostCall {
	t.Helper()
	var calls []hostCall
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		sql := m["sql"].(string)
		if !sqlGuardOK(sql) {
			t.Errorf("SQL rejected by the guard: %s", sql)
		}
		calls = append(calls, hostCall{fn, sql, m["args"].([]any)})
		switch fn {
		case "db.exec":
			n := int64(0)
			if len(affected) > 0 {
				n, affected = affected[0], affected[1:]
			}
			return json.Unmarshal([]byte(`{"rows_affected":`+itoa(n)+`}`), out)
		case "db.query":
			return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
		}
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	return &calls
}

func itoa(n int64) string { b, _ := json.Marshal(n); return string(b) }

func TestInsertPaymentReportsWhetherARowWasAdded(t *testing.T) {
	p := payment{EventID: "evt_1", SessionID: "cs_1", Amount: 5000, Currency: "aud", Label: "Donate", Kind: "donation", LinkID: "plink_1", Created: time.Unix(1730000000, 0)}
	calls := dbHost(t, []int64{1, 0}, "null")
	for i, want := range []bool{true, false} {
		got, err := insertPayment(p)
		if err != nil || got != want {
			t.Fatalf("call %d: %v %v", i, got, err)
		}
	}
	if c := (*calls)[0]; c.args[0] != "evt_1" || c.args[9] != int64(1730000000) {
		t.Fatalf("args %v", c.args)
	}
}

func TestEventSeen(t *testing.T) {
	dbHost(t, nil, `{"seen":true}`)
	if seen, err := eventSeen("evt_1"); err != nil || !seen {
		t.Fatalf("%v %v", seen, err)
	}
	dbHost(t, nil, `"{\"seen\":false}"`) // a jsonb cell may arrive as a string
	if seen, err := eventSeen("evt_1"); err != nil || seen {
		t.Fatalf("%v %v", seen, err)
	}
}

func TestListPaymentsPagesAndHasMore(t *testing.T) {
	rows := make([]string, pageSize+1)
	for i := range rows {
		rows[i] = `{"event_id":"evt_x","amount":100,"currency":"aud","created":"2026-11-03T10:00:00Z"}`
	}
	calls := dbHost(t, nil, "["+strings.Join(rows, ",")+"]")
	got, more, err := listPayments(3)
	if err != nil || !more || len(got) != pageSize {
		t.Fatalf("%d rows more=%v err=%v", len(got), more, err)
	}
	if c := (*calls)[0]; c.args[0] != pageSize+1 || c.args[1] != 2*pageSize {
		t.Fatalf("args %v", c.args)
	}
	if !got[0].Created.Equal(time.Date(2026, 11, 3, 10, 0, 0, 0, time.UTC)) {
		t.Fatalf("created %v", got[0].Created)
	}
}

func TestMonthTotalsKeepCurrenciesApart(t *testing.T) {
	dbHost(t, nil, `[{"month":"2026-11","currency":"aud","total":125000,"count":14},{"month":"2026-11","currency":"usd","total":9000,"count":2}]`)
	got, err := monthTotals()
	if err != nil || len(got) != 2 || got[0].Currency != "aud" || got[1].Total != 9000 {
		t.Fatalf("%+v %v", got, err)
	}
}

func TestPruneStopsAfterAShortBatch(t *testing.T) {
	calls := dbHost(t, []int64{pruneBatch, 7}, "null")
	if err := prune(24); err != nil {
		t.Fatal(err)
	}
	if len(*calls) != 2 || (*calls)[0].args[0] != 24 || (*calls)[0].args[1] != pruneBatch {
		t.Fatalf("calls %+v", *calls)
	}
}

func TestPruneIsBoundedPerRun(t *testing.T) {
	full := make([]int64, 20)
	for i := range full {
		full[i] = pruneBatch
	}
	calls := dbHost(t, full, "null")
	if err := prune(24); err != nil || len(*calls) != pruneBatches {
		t.Fatalf("%d calls, err %v", len(*calls), err)
	}
}

func TestOnPruneFailsOpen(t *testing.T) {
	callHost = func(fn string, _, _ any) error {
		return &sdk.HostError{Code: sdk.ErrUnavailable}
	}
	if err := onPrune(sdk.CronEvent{}); err != nil {
		t.Fatalf("onPrune returned %v", err)
	}
}

func TestFormatAmount(t *testing.T) {
	for _, c := range []struct {
		minor int64
		cur   string
		want  string
	}{
		{5000, "aud", "AUD 50.00"},
		{500, "JPY", "JPY 500"},
		{125000, "AUD", "AUD 1,250.00"},
		{5, "USD", "USD 0.05"},
		{0, "USD", "USD 0.00"},
		{-1050, "USD", "USD -10.50"},
		{1234, "KWD", "KWD 1.234"},
		{1234567, "JPY", "JPY 1,234,567"},
	} {
		if got := formatAmount(c.minor, c.cur); got != c.want {
			t.Errorf("formatAmount(%d, %s) = %q, want %q", c.minor, c.cur, got, c.want)
		}
	}
	if got := decimalAmount(125000, "AUD"); got != "1250.00" {
		t.Errorf("decimalAmount = %q", got)
	}
}
