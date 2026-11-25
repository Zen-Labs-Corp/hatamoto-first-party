package main

import (
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

func pct(name, email string, bp int64) splitRule {
	return splitRule{Name: name, Email: email, Kind: "percent", Value: bp}
}

func TestPeriodRange(t *testing.T) {
	s, e, ok := periodRange("2026-06")
	if !ok || s != time.Date(2026, 6, 1, 0, 0, 0, 0, time.UTC) || e != time.Date(2026, 7, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("june: %v %v %v", s, e, ok)
	}
	if _, e, _ := periodRange("2026-12"); e != time.Date(2027, 1, 1, 0, 0, 0, 0, time.UTC) {
		t.Errorf("december ends %v", e)
	}
	for _, bad := range []string{"", "2026-13", "2026-00", "26-06", "2026-6", "2026-06-01", "1999-01"} {
		if _, _, ok := periodRange(bad); ok {
			t.Errorf("%q accepted", bad)
		}
	}
}

func TestRuleOwed(t *testing.T) {
	for name, c := range map[string]struct {
		rule splitRule
		agg  eventAgg
		want int64
	}{
		"mockup organizer": {pct("a", "a@x.org", 8000), eventAgg{Gross: 420000, Refunds: 12000, Fees: 8400, Base: 399600}, 319680},
		"mockup co-host":   {pct("b", "b@x.org", 1000), eventAgg{Gross: 145000, Fees: 2900, Base: 142100}, 14210},
		"half rounds up":   {pct("a", "a@x.org", 1000), eventAgg{Base: 5}, 1},
		"below half":       {pct("a", "a@x.org", 1000), eventAgg{Base: 4}, 0},
		"full refund":      {pct("a", "a@x.org", 10000), eventAgg{Gross: 1100, Refunds: 1100, Fees: 100, Base: 0}, 0},
		"fixed":            {splitRule{Kind: "fixed", Value: 500}, eventAgg{Base: 5000, Tickets: 3}, 1500},
		"fixed clamped":    {splitRule{Kind: "fixed", Value: 500}, eventAgg{Base: 1200, Tickets: 3}, 1200},
		"fixed no base":    {splitRule{Kind: "fixed", Value: 500}, eventAgg{Base: 0, Tickets: 3}, 0},
		"jpy":              {pct("a", "a@x.org", 2500), eventAgg{Currency: "JPY", Base: 10001}, 2500},
	} {
		if got := ruleOwed(c.rule, c.agg); got != c.want {
			t.Errorf("%s: %d, want %d", name, got, c.want)
		}
	}
}

func TestBuildPayoutsMockup(t *testing.T) {
	aggs := []eventAgg{
		{EventID: "e1", Currency: "USD", Gross: 300000, Refunds: 12000, Fees: 6000, Base: 282000, Tickets: 60},
		{EventID: "e2", Currency: "USD", Gross: 120000, Fees: 2400, Base: 117600, Tickets: 20},
		{EventID: "e3", Currency: "USD", Gross: 145000, Fees: 2900, Base: 142100, Tickets: 29},
	}
	rules := map[string][]splitRule{
		"e1": {pct("Jazz Society", "Pay@Example.org", 8000)},
		"e2": {pct("Jazz Society", "pay@example.org ", 8000)},
		"e3": {pct("Jazz Society", "pay@example.org", 8000), pct("Co-host Ana", "ana@example.org", 1000)},
	}
	rows := buildPayouts(aggs, rules, nil)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	ana, jazz := rows[0], rows[1]
	if ana.Name != "Co-host Ana" || ana.Events != 1 || ana.Gross != 145000 || ana.Fees != 2900 || ana.Owed != 14210 || ana.Share != (payeeShare{"percent", 1000}) || ana.Paid != nil {
		t.Errorf("ana %+v", ana)
	}
	// (282000+117600+142100) x 80 %, rounded per event: 225600 + 94080 + 113680
	if jazz.Email != "pay@example.org" || jazz.Events != 3 || jazz.Gross != 565000 || jazz.Refunds != 12000 || jazz.Owed != 433360 || jazz.Share != (payeeShare{"percent", 8000}) {
		t.Errorf("jazz %+v", jazz)
	}
}

func TestBuildPayoutsGroupsCurrencyAndShare(t *testing.T) {
	aggs := []eventAgg{
		{EventID: "e1", Currency: "USD", Base: 1000},
		{EventID: "e1", Currency: "JPY", Base: 5000},
		{EventID: "e2", Currency: "USD", Base: 1000, Tickets: 2},
	}
	rules := map[string][]splitRule{
		"e1": {pct("Zed", "z@example.org", 5000)},
		"e2": {{Name: "Zed", Email: "z@example.org", Kind: "fixed", Value: 100}},
	}
	rows := buildPayouts(aggs, rules, nil)
	if len(rows) != 2 || rows[0].Currency != "JPY" || rows[1].Currency != "USD" {
		t.Fatalf("rows %+v", rows)
	}
	if rows[0].Owed != 2500 || rows[0].Share.Kind != "percent" {
		t.Errorf("jpy %+v", rows[0])
	}
	if rows[1].Owed != 700 || rows[1].Events != 2 || rows[1].Share.Kind != "mixed" {
		t.Errorf("usd %+v", rows[1])
	}
}

func TestBuildPayoutsPaidRecords(t *testing.T) {
	aggs := []eventAgg{{EventID: "e1", Currency: "USD", Base: 10000}}
	rules := map[string][]splitRule{"e1": {pct("Jazz", "j@example.org", 5000)}}
	paid := []payoutRecord{
		{Email: "J@example.org", Currency: "USD", Owed: 5000, PaidOn: "2026-07-02", Reference: "TR-88"},
		{Email: "gone@example.org", Name: "Gone", Currency: "USD", Owed: 700, PaidOn: "2026-07-03"},
	}
	rows := buildPayouts(aggs, rules, paid)
	if len(rows) != 2 {
		t.Fatalf("rows %+v", rows)
	}
	if p := rows[1].Paid; rows[1].Name != "Jazz" || p == nil || p.Changed || p.Reference != "TR-88" || p.PaidCents != 5000 {
		t.Errorf("jazz %+v", rows[1])
	}
	if p := rows[0].Paid; rows[0].Name != "Gone" || rows[0].Owed != 0 || p == nil || !p.Changed {
		t.Errorf("gone %+v", rows[0])
	}
	paid[0].Owed = 4000
	if p := buildPayouts(aggs, rules, paid[:1])[0].Paid; !p.Changed {
		t.Error("a late refund changed the amount, the row says so")
	}
}

// payoutsHost fakes the host for the report: one answer per statement.
type payoutsHost struct {
	aggs, held, rules, paid string
	calls                   []call
}

func newPayoutsHost(t *testing.T) *payoutsHost {
	t.Helper()
	h := &payoutsHost{aggs: `[]`, held: `0`, rules: `[]`, paid: `[]`}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if fn == sdk.FnDBQuery || fn == sdk.FnDBExec {
			h.calls = append(h.calls, call{fn, sql, ar})
		}
		cell := func(v string) error { return jsonInto(`{"rows":[[`+v+`]]}`, out) }
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case strings.Contains(sql, "FROM orders o LEFT JOIN events e") && strings.Contains(sql, "greatest(o.total_cents - o.refunded_cents - o.fee_cents, 0)"):
			return cell(h.aggs)
		case strings.Contains(sql, "problem IS NOT NULL"):
			return cell(h.held)
		case strings.Contains(sql, "FROM split_rules"):
			return cell(h.rules)
		case strings.Contains(sql, "FROM payouts WHERE period"):
			return cell(h.paid)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

func (h *payoutsHost) stmt(sub string) *call {
	for i, c := range h.calls {
		if strings.Contains(c.sql, sub) {
			return &h.calls[i]
		}
	}
	return nil
}

func payoutsGet(t *testing.T, u *sdk.HTTPUser, period string) (int, map[string]any) {
	return get(t, "/admin/payouts", "", u, map[string][]string{"period": {period}})
}

func TestPayoutsReport(t *testing.T) {
	h := newPayoutsHost(t)
	h.aggs = `[{"event_id":"e1","currency":"USD","event_name":"Gig","gross":420000,"refunds":12000,"fees":8400,"base":399600,"tickets":40},
{"event_id":"e2","currency":"JPY","event_name":"Fest","gross":10000,"refunds":0,"fees":0,"base":10000,"tickets":2}]`
	h.held = `2`
	h.rules = `[{"event_id":"e1","id":1,"name":"Jazz Society","email":"pay@example.org","kind":"percent","value":8000},
{"event_id":"e2","id":2,"name":"Jazz Society","email":"pay@example.org","kind":"percent","value":8000}]`
	h.paid = `[{"email":"pay@example.org","name":"Jazz Society","currency":"USD","owed":319680,"paid_on":"2026-07-02","reference":"TR-88"}]`
	code, body := payoutsGet(t, owner, "2026-06")
	if code != 200 || body["period"] != "2026-06" || body["held_back"] != float64(2) {
		t.Fatalf("%d %v", code, body)
	}
	exp := body["exponents"].(map[string]any)
	if exp["USD"] != float64(2) || exp["JPY"] != float64(0) {
		t.Errorf("exponents %v", exp)
	}
	rows := body["payees"].([]any)
	if len(rows) != 2 {
		t.Fatalf("payees %v", rows)
	}
	jpy, usd := rows[0].(map[string]any), rows[1].(map[string]any)
	if jpy["currency"] != "JPY" || jpy["owed"] != float64(8000) || jpy["paid"] != nil {
		t.Errorf("jpy %v", jpy)
	}
	paid := usd["paid"].(map[string]any)
	if usd["owed"] != float64(319680) || paid["reference"] != "TR-88" || paid["changed"] != false {
		t.Errorf("usd %v", usd)
	}
	st := h.stmt("FROM orders o LEFT JOIN events e")
	if st.args[0] != "2026-06-01T00:00:00Z" || st.args[1] != "2026-07-01T00:00:00Z" {
		t.Errorf("period args %v", st.args)
	}
	for _, want := range []string{"o.problem IS NULL", "o.status IN ('paid', 'refunded', 'cancelled')", "o.total_cents > 0"} {
		if !strings.Contains(st.sql, want) {
			t.Errorf("aggregate lacks %q", want)
		}
	}
}

func TestPayoutsReportEmpty(t *testing.T) {
	h := newPayoutsHost(t)
	code, body := payoutsGet(t, owner, "2026-06")
	if code != 200 || len(body["payees"].([]any)) != 0 {
		t.Errorf("%d %v", code, body)
	}
	if h.stmt("FROM split_rules") != nil {
		t.Error("no orders, no rule lookup")
	}
}

func TestPayoutsBadPeriodAndRoles(t *testing.T) {
	h := newPayoutsHost(t)
	for _, p := range []string{"2026-13", "26-06", ""} {
		if code, _ := payoutsGet(t, owner, p); code != 400 {
			t.Errorf("period %q: %d", p, code)
		}
	}
	for _, u := range []*sdk.HTTPUser{editor, nil} {
		if code, _ := payoutsGet(t, u, "2026-06"); code != 403 {
			t.Errorf("as %v: %d", u, code)
		}
	}
	if len(h.calls) != 0 {
		t.Errorf("a refused request read the database: %d calls", len(h.calls))
	}
}
