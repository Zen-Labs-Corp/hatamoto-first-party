package main

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"testing"
	"time"
)

// placeholders checks that sql uses $n parameters for every argument.
func placeholders(t *testing.T, c call) {
	t.Helper()
	oneStatement(t, c.sql())
	for i := range c.params() {
		if !strings.Contains(c.sql(), "$"+string(rune('1'+i))) {
			t.Fatalf("argument %d has no placeholder: %s", i+1, c.sql())
		}
	}
}

func TestListAndGetMonitor(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		return cell(out, `[{"id":7,"name":"Site","url":"https://example.org/","interval_s":300,
			"is_site":true,"status":"up","next_check":"2026-01-02T03:04:05.5+00:00","last_check":null,"last_ms":120}]`)
	})
	ms, err := listMonitors()
	if err != nil || len(ms) != 1 || ms[0].ID != 7 || !ms[0].IsSite || ms[0].LastMS == nil || *ms[0].LastMS != 120 || ms[0].LastCheck != nil {
		t.Fatalf("got %+v %v", ms, err)
	}
	m, ok, err := getMonitor(7)
	if err != nil || !ok || m.Name != "Site" {
		t.Fatalf("got %+v %v %v", m, ok, err)
	}
	if p := f.calls[1].params(); len(p) != 1 || p[0] != float64(7) {
		t.Fatalf("params %v", p)
	}
	placeholders(t, f.calls[1])

	newFakeHost(t, func(c call, out any) error { return cell(out, `[]`) })
	if _, ok, err := getMonitor(9); ok || err != nil {
		t.Fatalf("unknown id: %v %v", ok, err)
	}
}

func TestCountAndInsertMonitor(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		if strings.HasPrefix(c.sql(), "WITH") {
			return cell(out, `42`)
		}
		return cell(out, `3`)
	})
	if n, err := countMonitors(); err != nil || n != 3 {
		t.Fatalf("count %d %v", n, err)
	}
	id, err := insertMonitor(monitor{Name: "x'; DROP TABLE monitors; --", URL: "https://example.org", IntervalS: 60, Keyword: "k", MaxMS: 500, IsSite: true})
	if err != nil || id != 42 {
		t.Fatalf("insert %d %v", id, err)
	}
	c := f.calls[1]
	placeholders(t, c)
	if strings.Contains(c.sql(), "DROP") {
		t.Fatal("input leaked into the SQL text")
	}
	if p := c.params(); len(p) != 6 || p[0] != "x'; DROP TABLE monitors; --" || p[2] != float64(60) || p[5] != true {
		t.Fatalf("params %v", p)
	}
}

func TestUpdateAndDeleteMonitor(t *testing.T) {
	f := newFakeHost(t, nil)
	if err := updateMonitor(monitor{ID: 3, Name: "n", URL: "https://example.org", IntervalS: 300}); err != nil {
		t.Fatal(err)
	}
	if err := deleteMonitor(3); err != nil {
		t.Fatal(err)
	}
	q := f.sqls()
	if len(q) != 5 {
		t.Fatalf("want update plus four deletes, got %v", q)
	}
	for _, c := range f.calls {
		placeholders(t, c)
	}
	for i, table := range []string{"checks", "incidents", "daily", "monitors"} {
		if !strings.HasPrefix(q[i+1], "DELETE FROM "+table+" ") {
			t.Fatalf("statement %d: %s", i+1, q[i+1])
		}
	}

	f = newFakeHost(t, func(call, any) error { return errors.New("down") })
	if err := deleteMonitor(3); err == nil || len(f.sqls()) != 1 {
		t.Fatalf("must stop at the first failure: %v %v", err, f.sqls())
	}
}

func TestRecordCheck(t *testing.T) {
	f := newFakeHost(t, nil)
	at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	okv := true
	if err := recordCheck(7, at, 200, 150, &okv, ""); err != nil {
		t.Fatal(err)
	}
	if err := recordCheck(7, at, 0, 0, nil, "blocked"); err != nil {
		t.Fatal(err)
	}
	placeholders(t, f.calls[0])
	p := f.calls[0].params()
	if p[1] != "2026-01-02T03:04:05Z" || p[4] != true {
		t.Fatalf("params %v", p)
	}
	if got := f.calls[1].params()[4]; got != nil {
		t.Fatalf("config check must store ok NULL, got %v", got)
	}
}

func TestCertRoundTrip(t *testing.T) {
	f := newFakeHost(t, func(c call, out any) error {
		return cell(out, `[{"host":"example.org","not_after":"2026-03-01T00:00:00+00:00","issuer":"R3","lookup_error":"","warned":[21,7]}]`)
	})
	c, ok, err := getCert("example.org")
	if err != nil || !ok || c.NotAfter == nil || c.Issuer != "R3" || len(c.Warned) != 2 || c.LookedUpAt != nil {
		t.Fatalf("got %+v %v %v", c, ok, err)
	}
	na := time.Date(2026, 3, 1, 0, 0, 0, 0, time.UTC)
	if err := upsertCert(cert{Host: "example.org", NotAfter: &na, Issuer: "R3"}); err != nil {
		t.Fatal(err)
	}
	last := f.calls[len(f.calls)-1]
	placeholders(t, last)
	if !regexp.MustCompile(`ON CONFLICT \(host\) DO UPDATE`).MatchString(last.sql()) {
		t.Fatalf("not an upsert: %s", last.sql())
	}
	p := last.params()
	if p[1] != "2026-03-01T00:00:00Z" || p[3] != nil {
		t.Fatalf("params %v", p)
	}
	var warned []int
	if err := json.Unmarshal([]byte(p[5].(string)), &warned); err != nil || warned == nil || len(warned) != 0 {
		t.Fatalf("empty warned must be [], got %v %v", p[5], err)
	}

	newFakeHost(t, func(c call, out any) error { return cell(out, `[]`) })
	if _, ok, err := getCert("other.example"); ok || err != nil {
		t.Fatalf("unknown host: %v %v", ok, err)
	}
}
