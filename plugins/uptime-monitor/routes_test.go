package main

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const secretHeartbeat = "https://hb.example.net/ping/route-secret-token"

// routeHost fakes the database, kv, the web and mail for the admin routes.
type routeHost struct {
	t        *testing.T
	clock    time.Time
	monitors []*monitor
	nextID   int64
	st       settings
	granted  []string
	kv       map[string]json.RawMessage
	status   int // HTTP status every monitor fetch answers
	fetched  []string
	checks   int
	mailed   [][]string
	mailErr  error
	dbErr    error
	stats    string
	daily    string
	certs    string
	incident string
	deleted  []int64
	f        *fakeHost
}

func newRouteHost(t *testing.T) *routeHost {
	t.Helper()
	h := &routeHost{t: t, nextID: 1, status: 200, kv: map[string]json.RawMessage{},
		st:      settings{AlertEmails: []string{"ops@example.org"}, FailThreshold: 2, SSLWarnDays: []int{21, 7, 1}, HeartbeatURL: secretHeartbeat},
		granted: append([]string(nil), requiredPermissions...),
		stats:   `[]`, daily: `[]`, certs: `[]`, incident: `[]`}
	h.clock = setNow(t, "2026-03-10T12:00:00Z")
	h.f = newFakeHost(t, h.reply)
	now = func() time.Time { return h.clock }
	return h
}

func (h *routeHost) add(m monitor) *monitor {
	m.ID = h.nextID
	h.nextID++
	if m.IntervalS == 0 {
		m.IntervalS = 300
	}
	h.monitors = append(h.monitors, &m)
	return &m
}

func (h *routeHost) find(id int64) *monitor {
	for _, m := range h.monitors {
		if m.ID == id {
			return m
		}
	}
	return nil
}

func (h *routeHost) reply(c call, out any) error {
	sql := c.sql()
	oneStatement(h.t, sql)
	switch c.Fn {
	case "settings.get":
		b, _ := json.Marshal(h.st)
		return json.Unmarshal(b, out)
	case "permissions":
		b, _ := json.Marshal(map[string]any{"granted": h.granted})
		return json.Unmarshal(b, out)
	case "kv.set":
		h.kv[c.Args["key"].(string)], _ = json.Marshal(c.Args["value"])
	case "kv.delete":
		delete(h.kv, c.Args["key"].(string))
	case "kv.get":
		v, ok := h.kv[c.Args["key"].(string)]
		if !ok {
			return json.Unmarshal([]byte(`{"found":false}`), out)
		}
		b, _ := json.Marshal(map[string]any{"found": true, "value": v})
		return json.Unmarshal(b, out)
	case "http.fetch":
		h.fetched = append(h.fetched, c.Args["url"].(string))
		h.clock = h.clock.Add(120 * time.Millisecond)
		return json.Unmarshal([]byte(`{"status":`+itoa(h.status)+`}`), out)
	case "mail.send":
		if h.mailErr != nil {
			return h.mailErr
		}
		var to []string
		for _, a := range c.Args["to"].([]any) {
			to = append(to, a.(string))
		}
		h.mailed = append(h.mailed, to)
	case "db.query", "db.exec":
		if h.dbErr != nil {
			return h.dbErr
		}
		return h.db(c, sql, out)
	}
	return nil
}

func itoa(n int) string { b, _ := json.Marshal(n); return string(b) }

func (h *routeHost) db(c call, sql string, out any) error {
	p := c.params()
	switch {
	case strings.Contains(sql, "FROM monitors m WHERE m.id"):
		var found []monitor
		if m := h.find(int64(p[0].(float64))); m != nil {
			found = append(found, *m)
		}
		b, _ := json.Marshal(found)
		return cell(out, string(b))
	case strings.Contains(sql, "FROM monitors m"):
		all := []monitor{}
		for _, m := range h.monitors {
			all = append(all, *m)
		}
		b, _ := json.Marshal(all)
		return cell(out, string(b))
	case strings.Contains(sql, "INSERT INTO monitors"):
		m := h.add(monitor{Name: p[0].(string), URL: p[1].(string), IntervalS: int(p[2].(float64)),
			Keyword: p[3].(string), MaxMS: int(p[4].(float64)), IsSite: p[5].(bool), Status: statusPending})
		return cell(out, itoa(int(m.ID)))
	case strings.HasPrefix(sql, "UPDATE monitors SET name"):
		m := h.find(int64(p[0].(float64)))
		m.Name, m.URL, m.IntervalS = p[1].(string), p[2].(string), int(p[3].(float64))
		m.Keyword, m.MaxMS, m.IsSite = p[4].(string), int(p[5].(float64)), p[6].(bool)
	case strings.HasPrefix(sql, "UPDATE monitors SET status"):
		m := h.find(int64(p[0].(float64)))
		m.Status = p[1].(string)
		at, _ := time.Parse(time.RFC3339, p[2].(string))
		m.LastCheck = &at
	case strings.HasPrefix(sql, "DELETE FROM monitors"):
		id := int64(p[0].(float64))
		h.deleted = append(h.deleted, id)
		for i, m := range h.monitors {
			if m.ID == id {
				h.monitors = append(h.monitors[:i], h.monitors[i+1:]...)
				break
			}
		}
	case strings.HasPrefix(sql, "INSERT INTO checks"):
		h.checks++
	case strings.Contains(sql, "AS n24"):
		return cell(out, h.stats)
	case strings.Contains(sql, "FROM daily WHERE"):
		return cell(out, h.daily)
	case strings.Contains(sql, "FROM certs c"):
		return cell(out, h.certs)
	case strings.Contains(sql, "FROM incidents i LEFT JOIN"):
		return cell(out, h.incident)
	case strings.Contains(sql, "FROM incidents"), strings.Contains(sql, "FROM outbox ORDER BY id"):
		return cell(out, `[]`)
	case strings.HasPrefix(sql, "WITH d AS (DELETE FROM outbox"):
		return cell(out, `0`)
	}
	return nil
}

func (h *routeHost) do(method, path, body string) (int, map[string]any) {
	h.t.Helper()
	res, err := handleRoute(sdk.HTTPRequest{Method: method, Path: path, Pattern: path, Body: body})
	if err != nil {
		h.t.Fatal(err)
	}
	if res.Headers["Content-Type"] != "application/json" {
		h.t.Fatalf("content type %q", res.Headers["Content-Type"])
	}
	if strings.Contains(res.Body, secretHeartbeat) || strings.Contains(res.Body, "route-secret-token") {
		h.t.Fatalf("heartbeat URL in the %s %s response", method, path)
	}
	var v map[string]any
	if err := json.Unmarshal([]byte(res.Body), &v); err != nil {
		h.t.Fatalf("not JSON: %q", res.Body)
	}
	return res.Status, v
}

const goodMonitor = `{"name":"Shop","url":"https://shop.example.org/","interval":"5m"}`

func TestOverviewShapeAndNoHeartbeatURL(t *testing.T) {
	h := newRouteHost(t)
	down := h.clock.Add(-12 * time.Minute)
	last := h.clock.Add(-time.Minute)
	m1 := h.add(monitor{Name: "This site", URL: "https://example.org/", IsSite: true, Status: statusUp, LastCheck: &last})
	m2 := h.add(monitor{Name: "Shop", URL: "https://shop.example.org/", Status: statusDown, LastError: "HTTP 503", DownSince: &down, LastCheck: &last})
	h.add(monitor{Name: "Intranet", URL: "http://10.0.0.5/", Status: statusConfig, LastError: "blocked address"})
	h.stats = `[{"monitor_id":1,"n24":10,"up24":10,"n30":2000,"up30":1999,"n_today":4,"up_today":4,"avg_ms":212},
		{"monitor_id":2,"n24":10,"up24":9,"n30":10,"up30":9,"n_today":0,"up_today":0,"avg_ms":null}]`
	h.daily = `[{"monitor_id":1,"day":"2026-03-09","pct":99.5},{"monitor_id":1,"day":"2026-01-01","pct":100}]`
	h.certs = `[{"host":"example.org","not_after":"2026-05-04T00:00:00Z","issuer":"CN=X","looked_up_at":"2026-03-10T00:00:00Z","lookup_error":"","warned":[]},
		{"host":"shop.example.org","not_after":"2026-01-01T00:00:00Z","issuer":"","looked_up_at":"2026-03-10T00:00:00Z","lookup_error":"unknown","warned":[]}]`
	h.incident = `[{"id":7,"monitor_id":2,"name":"Shop","started_at":"2026-03-10T11:48:00Z","ended_at":null,"cause":"HTTP 503"}]`
	h.f.calls = nil

	status, v := h.do("GET", "/overview", "")
	if status != 200 || v["db_ready"] != true {
		t.Fatalf("%d %v", status, v)
	}
	ms := v["monitors"].([]any)
	if len(ms) != 3 {
		t.Fatalf("%v", ms)
	}
	site := ms[0].(map[string]any)
	if site["id"].(float64) != float64(m1.ID) || site["status"] != "up" || site["is_site"] != true || site["interval"] != "5m" {
		t.Fatalf("%v", site)
	}
	if site["uptime_24h"].(float64) != 100 || site["uptime_30d"].(float64) != 99.95 || site["avg_ms"].(float64) != 212 {
		t.Fatalf("%v", site)
	}
	if site["cert_days_left"].(float64) != 54 {
		t.Fatalf("cert days %v", site["cert_days_left"])
	}
	if bar := site["bar"].(string); !strings.HasPrefix(bar, "data:image/svg+xml") {
		t.Fatalf("bar %.30s", bar)
	}
	shop := ms[1].(map[string]any)
	if shop["id"].(float64) != float64(m2.ID) || shop["status"] != "down" || shop["cause"] != "HTTP 503" || shop["since"] == nil {
		t.Fatalf("%v", shop)
	}
	if shop["cert_unknown"] != true || site["cert_unknown"] != false {
		t.Fatalf("cert_unknown: shop %v site %v", shop["cert_unknown"], site["cert_unknown"])
	}
	if shop["cert_days_left"] != nil || shop["avg_ms"] != nil || shop["uptime_24h"].(float64) != 90 {
		t.Fatalf("%v", shop)
	}
	cfg := ms[2].(map[string]any)
	if cfg["uptime_24h"] != nil || cfg["uptime_30d"] != nil || cfg["cause"] != "blocked address" || cfg["cert_days_left"] != nil {
		t.Fatalf("%v", cfg)
	}
	if inc := v["incidents"].([]any); len(inc) != 1 || inc[0].(map[string]any)["name"] != "Shop" {
		t.Fatalf("%v", v["incidents"])
	}
	s := v["settings"].(map[string]any)
	if s["alert_emails"].(float64) != 1 || s["heartbeat"] != true || s["fail_threshold"].(float64) != 2 || s["mail_configured"] != true || len(s["ssl_warn_days"].([]any)) != 3 {
		t.Fatalf("%v", s)
	}
	if _, has := s["heartbeat_url"]; has {
		t.Fatal("heartbeat_url in settings")
	}
	if miss := v["missing"].([]any); len(miss) != 0 {
		t.Fatalf("missing %v", miss)
	}
	for _, l := range h.f.logs() {
		if strings.Contains(l, "route-secret-token") {
			t.Fatalf("log %q", l)
		}
	}
}

func TestOverviewBarUsesTheDailyRowsAndToday(t *testing.T) {
	h := newRouteHost(t)
	h.add(monitor{Name: "A", URL: "http://a.example.org/", Status: statusUp})
	h.stats = `[{"monitor_id":1,"n24":4,"up24":4,"n30":4,"up30":4,"n_today":2,"up_today":1,"avg_ms":10}]`
	h.daily = `[{"monitor_id":1,"day":"2026-03-09","pct":100},{"monitor_id":1,"day":"2025-12-11","pct":98}]`
	_, v := h.do("GET", "/overview", "")
	svg := decodeBar(t, v["monitors"].([]any)[0].(map[string]any)["bar"].(string))
	fills := strings.Split(svg, `fill="`)[1:]
	at := func(i int) string { return fills[i][:7] }
	// 2026-03-10 is the last cell, 03-09 the one before, 2025-12-11 the first.
	if at(89) != colorRed || at(88) != colorGreen || at(0) != colorRed || at(50) != colorGrey {
		t.Fatalf("cells %s %s %s %s", at(0), at(50), at(88), at(89))
	}
}

func TestOverviewReportsMissingPermissionsAndMailState(t *testing.T) {
	h := newRouteHost(t)
	h.granted = []string{"settings", "kv", "log", "db"}
	h.kv[mailUnavailableKey] = json.RawMessage(`true`)
	_, v := h.do("GET", "/overview", "")
	var miss []string
	for _, m := range v["missing"].([]any) {
		miss = append(miss, m.(string))
	}
	if strings.Join(miss, ",") != "http.fetch,http.fetch.any,mail.send" {
		t.Fatalf("missing %v", miss)
	}
	if v["settings"].(map[string]any)["mail_configured"] != false {
		t.Fatalf("%v", v["settings"])
	}
}

func TestOverviewWithoutDatabaseIsEmptyNotAnError(t *testing.T) {
	h := newRouteHost(t)
	h.dbErr = hostErr(sdk.ErrCapabilityDenied, "db not granted")
	status, v := h.do("GET", "/overview", "")
	if status != 200 || v["db_ready"] != false || len(v["monitors"].([]any)) != 0 || len(v["incidents"].([]any)) != 0 {
		t.Fatalf("%d %v", status, v)
	}
}

func TestMonitorsCreateUpdateDelete(t *testing.T) {
	h := newRouteHost(t)
	status, v := h.do("POST", "/monitors", `{"action":"create","monitor":`+goodMonitor+`}`)
	if status != 200 || v["id"].(float64) != 1 || len(h.monitors) != 1 || h.monitors[0].IntervalS != 300 || h.monitors[0].Name != "Shop" {
		t.Fatalf("%d %v", status, v)
	}
	status, _ = h.do("POST", "/monitors", `{"action":"update","id":1,"monitor":{"name":"Shop 2","url":"http://shop.example.org/","interval":"15m","keyword":"Cart","max_ms":900,"is_site":true}}`)
	m := h.monitors[0]
	if status != 200 || m.Name != "Shop 2" || m.IntervalS != 900 || m.Keyword != "Cart" || m.MaxMS != 900 || !m.IsSite {
		t.Fatalf("%d %+v", status, m)
	}
	// The site monitor can be saved again: it does not clash with itself.
	if status, v = h.do("POST", "/monitors", `{"action":"update","id":1,"monitor":{"name":"Shop 3","url":"http://shop.example.org/","is_site":true}}`); status != 200 {
		t.Fatalf("%d %v", status, v)
	}
	if status, _ = h.do("POST", "/monitors", `{"action":"delete","id":1}`); status != 200 || len(h.monitors) != 0 || len(h.deleted) != 1 {
		t.Fatalf("%d %v", status, h.monitors)
	}
	childDeletes := 0
	for _, s := range h.f.sqls() {
		if strings.HasPrefix(s, "DELETE FROM checks") || strings.HasPrefix(s, "DELETE FROM incidents") || strings.HasPrefix(s, "DELETE FROM daily") {
			childDeletes++
		}
	}
	if childDeletes != 3 {
		t.Fatalf("child deletes %d", childDeletes)
	}
}

func TestMonitorsRejectBadInput(t *testing.T) {
	cases := map[string]string{
		"ftp url":         `{"action":"create","monitor":{"name":"A","url":"ftp://example.org/"}}`,
		"credentials":     `{"action":"create","monitor":{"name":"A","url":"https://user:pw@example.org/"}}`,
		"no host":         `{"action":"create","monitor":{"name":"A","url":"https:///x"}}`,
		"bad interval":    `{"action":"create","monitor":{"name":"A","url":"https://example.org/","interval":"2m"}}`,
		"empty name":      `{"action":"create","monitor":{"name":" ","url":"https://example.org/"}}`,
		"long keyword":    `{"action":"create","monitor":{"name":"A","url":"https://example.org/","keyword":"` + strings.Repeat("k", 201) + `"}}`,
		"max_ms low":      `{"action":"create","monitor":{"name":"A","url":"https://example.org/","max_ms":50}}`,
		"unknown key":     `{"action":"create","monitor":` + goodMonitor + `,"extra":1}`,
		"unknown nested":  `{"action":"create","monitor":{"name":"A","url":"https://example.org/","port":8080}}`,
		"missing monitor": `{"action":"create"}`,
		"bad action":      `{"action":"drop","monitor":` + goodMonitor + `}`,
		"not json":        `{"action":`,
		"trailing":        `{"action":"create","monitor":` + goodMonitor + `} {}`,
		"update no id":    `{"action":"update","monitor":` + goodMonitor + `}`,
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			h := newRouteHost(t)
			status, v := h.do("POST", "/monitors", body)
			if status != http.StatusBadRequest || v["error"] == "" || len(h.monitors) != 0 {
				t.Fatalf("%d %v", status, v)
			}
		})
	}
}

func TestMonitorsLimits(t *testing.T) {
	h := newRouteHost(t)
	for i := 0; i < 20; i++ {
		h.add(monitor{Name: "M", URL: "http://m.example.org/"})
	}
	if status, v := h.do("POST", "/monitors", `{"action":"create","monitor":`+goodMonitor+`}`); status != 400 || !strings.Contains(v["error"].(string), "20") {
		t.Fatalf("21st monitor: %d %v", status, v)
	}
	if status, _ := h.do("POST", "/monitors", `{"action":"update","id":3,"monitor":`+goodMonitor+`}`); status != 200 {
		t.Fatalf("editing at the limit: %d", status)
	}

	h = newRouteHost(t)
	h.add(monitor{Name: "Site", URL: "https://example.org/", IsSite: true})
	if status, v := h.do("POST", "/monitors", `{"action":"create","monitor":{"name":"B","url":"https://b.example.org/","is_site":true}}`); status != 400 || !strings.Contains(v["error"].(string), "This site") {
		t.Fatalf("second site: %d %v", status, v)
	}
	h.add(monitor{Name: "Other", URL: "https://o.example.org/"})
	if status, _ := h.do("POST", "/monitors", `{"action":"update","id":2,"monitor":{"name":"B","url":"https://b.example.org/","is_site":true}}`); status != 400 {
		t.Fatalf("second site by update: %d", status)
	}
}

func TestMonitorsUnknownID(t *testing.T) {
	h := newRouteHost(t)
	for _, action := range []string{"update", "delete"} {
		if status, _ := h.do("POST", "/monitors", `{"action":"`+action+`","id":99,"monitor":`+goodMonitor+`}`); status != 404 {
			t.Fatalf("%s: %d", action, status)
		}
	}
}

func TestCheckNowUnknownIDAndBadBody(t *testing.T) {
	h := newRouteHost(t)
	if status, _ := h.do("POST", "/check-now", `{"id":5}`); status != 404 {
		t.Fatalf("%d", status)
	}
	if status, _ := h.do("POST", "/check-now", `{"id":1,"x":1}`); status != 400 {
		t.Fatalf("%d", status)
	}
	if status, _ := h.do("POST", "/check-now", `nope`); status != 400 {
		t.Fatalf("%d", status)
	}
}

func TestCheckNowReusesAResultUnder10SecondsOld(t *testing.T) {
	h := newRouteHost(t)
	last := h.clock.Add(-9 * time.Second)
	h.add(monitor{Name: "A", URL: "http://a.example.org/", Status: statusUp, LastCheck: &last})
	status, v := h.do("POST", "/check-now", `{"id":1}`)
	if status != 200 || len(h.fetched) != 0 || v["status"] != "up" {
		t.Fatalf("%d %v fetched %v", status, v, h.fetched)
	}
}

func TestCheckNowRunsTheCheckAndRecordsIt(t *testing.T) {
	h := newRouteHost(t)
	last := h.clock.Add(-11 * time.Second)
	h.add(monitor{Name: "A", URL: "http://a.example.org/", Status: statusUp, LastCheck: &last})
	h.status = 503
	status, v := h.do("POST", "/check-now", `{"id":1}`)
	if status != 200 || len(h.fetched) != 1 || h.checks != 1 || v["status"] != "down" || v["cause"] != "HTTP 503" {
		t.Fatalf("%d %v fetched %v checks %d", status, v, h.fetched, h.checks)
	}
	var timeout float64
	for _, c := range h.f.calls {
		if c.Fn == "http.fetch" {
			timeout = c.Args["timeout_ms"].(float64)
		}
	}
	if timeout != checkNowTimeoutMS || timeout > 6000 {
		t.Fatalf("timeout %v", timeout)
	}
}

func TestCheckNowLeavesMailForTheNextTick(t *testing.T) {
	h := newRouteHost(t)
	h.st.FailThreshold = 1
	last := h.clock.Add(-time.Minute)
	h.add(monitor{Name: "A", URL: "http://a.example.org/", Status: statusUp, LastCheck: &last})
	h.status = 503
	if status, v := h.do("POST", "/check-now", `{"id":1}`); status != 200 || v["status"] != "down" {
		t.Fatalf("%d %v", status, v)
	}
	if len(h.mailed) != 0 || h.f.count("mail.send") != 0 {
		t.Fatalf("mail sent inside the route: %v", h.mailed)
	}
	queued := 0
	for _, q := range h.f.sqls() {
		if strings.Contains(q, "FROM outbox") {
			t.Fatalf("the route read the outbox: %s", q)
		}
		if strings.HasPrefix(q, "INSERT INTO outbox") {
			queued++
		}
	}
	if queued != 1 {
		t.Fatalf("%d mails queued, want 1", queued)
	}
}

func TestTestAlertNeedsAnAddress(t *testing.T) {
	h := newRouteHost(t)
	h.st.AlertEmails = nil
	if status, _ := h.do("POST", "/test-alert", `{}`); status != 400 || len(h.mailed) != 0 {
		t.Fatalf("%d", status)
	}
}

func TestTestAlertGoesThroughTheLimiter(t *testing.T) {
	h := newRouteHost(t)
	h.st.AlertEmails = []string{"a@example.org", "b@example.org", "c@example.org"}
	for i := 0; i < 2; i++ {
		if status, v := h.do("POST", "/test-alert", ``); status != 200 || v["sent"].(float64) != 3 {
			t.Fatalf("send %d: %d %v", i, status, v)
		}
	}
	status, v := h.do("POST", "/test-alert", `{}`)
	if status != 200 || v["sent"].(float64) != 0 || !strings.Contains(v["reason"].(string), "6 recipients per minute") || len(h.mailed) != 2 {
		t.Fatalf("%d %v mailed %d", status, v, len(h.mailed))
	}
	h.clock = h.clock.Add(61 * time.Second)
	if _, v = h.do("POST", "/test-alert", `{}`); v["sent"].(float64) != 3 {
		t.Fatalf("after the window: %v", v)
	}
}

func TestTestAlertMailUnavailableIsReported(t *testing.T) {
	h := newRouteHost(t)
	h.mailErr = hostErr(sdk.ErrUnavailable, "mail is off")
	status, v := h.do("POST", "/test-alert", `{}`)
	if status != 200 || v["sent"].(float64) != 0 || !strings.Contains(v["reason"].(string), "not configured") {
		t.Fatalf("%d %v", status, v)
	}
	_, o := h.do("GET", "/overview", "")
	if o["settings"].(map[string]any)["mail_configured"] != false {
		t.Fatalf("%v", o["settings"])
	}
	h.mailErr = nil
	h.clock = h.clock.Add(time.Minute)
	h.do("POST", "/test-alert", `{}`)
	_, o = h.do("GET", "/overview", "")
	if o["settings"].(map[string]any)["mail_configured"] != true {
		t.Fatal("flag not cleared after a mail went out")
	}
}

func TestUnknownRoutesAre404(t *testing.T) {
	h := newRouteHost(t)
	for _, r := range [][2]string{{"GET", "/nope"}, {"POST", "/overview"}, {"GET", "/monitors"}, {"DELETE", "/monitors"}} {
		if status, _ := h.do(r[0], r[1], ""); status != 404 {
			t.Fatalf("%s %s: %d", r[0], r[1], status)
		}
	}
}

func TestRoutesFailOpenWithoutDatabase(t *testing.T) {
	h := newRouteHost(t)
	h.dbErr = hostErr(sdk.ErrCapabilityDenied, "db not granted")
	for _, body := range []string{`{"action":"create","monitor":` + goodMonitor + `}`, `{"action":"delete","id":1}`} {
		if status, _ := h.do("POST", "/monitors", body); status != http.StatusServiceUnavailable {
			t.Fatalf("%d", status)
		}
	}
	if status, _ := h.do("POST", "/check-now", `{"id":1}`); status != http.StatusServiceUnavailable {
		t.Fatalf("%d", status)
	}
}
