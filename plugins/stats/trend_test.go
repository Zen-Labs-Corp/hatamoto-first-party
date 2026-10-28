package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func date(y int, m time.Month, d int) time.Time { return time.Date(y, m, d, 0, 0, 0, 0, time.UTC) }

func TestCutoffMonth(t *testing.T) {
	for in, want := range map[time.Time]string{
		date(2026, 10, 28): "2025-09-01",
		date(2026, 1, 1):   "2024-12-01", // January boundary
		date(2026, 1, 31):  "2024-12-01",
		date(2026, 3, 31):  "2025-02-01", // 31st of a month
		date(2028, 2, 29):  "2027-01-01", // leap day
		date(2026, 12, 15): "2025-11-01",
	} {
		if got := cutoffMonth(in); got != want {
			t.Errorf("cutoffMonth(%s) = %s, want %s", in.Format(dateFmt), got, want)
		}
	}
}

// The weekly points stop where their look-back would reach folded rows. What
// they leave out of the 13-month window is always less than a month, so a
// monthly part for it could never apply and the trend reads no monthly rows.
func TestTrendPlanNeedsNoMonthlyPart(t *testing.T) {
	for today := date(2024, 1, 1); today.Before(date(2030, 1, 1)); today = today.AddDate(0, 0, 1) {
		weeks := trendPlan(today)
		cutoff, _ := time.Parse(dateFmt, cutoffMonth(today))
		windowStart := today.AddDate(0, -trendMonths, 0)
		if len(weeks) < 45 || len(weeks) > 58 {
			t.Fatalf("%s: %d weeks", day(today), len(weeks))
		}
		if weeks[len(weeks)-1] != day(today) {
			t.Fatalf("%s: last week %s", day(today), weeks[len(weeks)-1])
		}
		first, _ := time.Parse(dateFmt, weeks[0])
		if !first.After(windowStart) || first.AddDate(0, 0, -trendLookbackDays).Before(cutoff) {
			t.Fatalf("%s: first week %s is outside the window or looks back past the cutoff", day(today), weeks[0])
		}
		covered := first.AddDate(0, 0, -trendLookbackDays+1)
		firstMonth := time.Date(windowStart.Year(), windowStart.Month(), 1, 0, 0, 0, 0, time.UTC)
		if !firstMonth.AddDate(0, 1, 0).After(covered) {
			t.Fatalf("%s: a whole month before %s would be left to monthly rows", day(today), day(covered))
		}
		for i := 1; i < len(weeks); i++ {
			a, _ := time.Parse(dateFmt, weeks[i-1])
			b, _ := time.Parse(dateFmt, weeks[i])
			if b.Sub(a) != 7*24*time.Hour {
				t.Fatalf("%s: gap before %s", day(today), weeks[i])
			}
		}
	}
}

func TestBuildTrendOrderLabelsAndArgs(t *testing.T) {
	today := date(2026, 10, 28)
	weeks := trendPlan(today)
	var calls []hostCall
	callHost = func(fn string, a, out any) error {
		m := a.(map[string]any)
		sql := m["sql"].(string)
		calls = append(calls, hostCall{fn, sql, m["args"].([]any)})
		cell := map[string]string{
			qWeekly: `[{"date":"` + weeks[0] + `","active":0},{"date":"2026-10-28","active":9}]`,
		}[sql]
		return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
	}
	got, err := buildTrend(today)
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].Kind != "week" || got[1].Label != "2026-10-28" || got[1].Active != 9 {
		t.Fatalf("points %+v", got)
	}
	if len(calls) != 1 || calls[0].args[0] != weeks[0] || calls[0].args[1] != len(weeks)-1 {
		t.Fatalf("calls %+v", calls)
	}
}

// qWeekly must stay a single pass over installs_daily: joining every week-end
// to 30 days of rows cost about 18 s for 10k installs and a year of pings.
func TestWeeklyQueryIsOnePassOverDailyRows(t *testing.T) {
	if n := strings.Count(qWeekly, "installs_daily"); n != 1 {
		t.Fatalf("qWeekly reads installs_daily %d times", n)
	}
	for _, want := range []string{"bit_or(", "GROUP BY d.install", "$1::date", "$2::int"} {
		if !strings.Contains(qWeekly, want) {
			t.Errorf("qWeekly lacks %q", want)
		}
	}
	if strings.Contains(qWeekly, "JOIN installs_daily") {
		t.Error("qWeekly joins installs_daily per week")
	}
}

// The week bits live in one bigint.
func TestTrendPlanFitsTheWeekBitmask(t *testing.T) {
	for d := date(2024, 1, 1); d.Before(date(2029, 1, 1)); d = d.AddDate(0, 0, 1) {
		if n := len(trendPlan(d)); n > 62 {
			t.Fatalf("%s: %d weeks", d.Format(dateFmt), n)
		}
	}
}
