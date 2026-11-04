package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// scanWorld is a fake site and a fake clock for the cron run.
type scanWorld struct {
	t        *testing.T
	clock    time.Time
	kv       map[string]json.RawMessage
	pages    [][]string          // entity attribute documents, one slice per list page
	due      map[string][]string // kind -> due link urls
	fetchDur time.Duration
	status   map[string]int // url -> http status (default 200)
	dbErr    error
	granted  string
	listErr  error
	listArgs []map[string]any
	fetched  []string
	fetchAt  []time.Time
	host     *fakeHost
}

func newScanWorld(t *testing.T) *scanWorld {
	w := &scanWorld{t: t, clock: time.Date(2026, 3, 1, 12, 0, 0, 0, time.UTC),
		kv: map[string]json.RawMessage{}, due: map[string][]string{}, status: map[string]int{}, granted: `{"granted":["http.fetch","http.fetch.any","db"]}`, fetchDur: 300 * time.Millisecond}
	w.host = newFakeHost(t, w.reply)
	now = func() time.Time { return w.clock }
	return w
}

func (w *scanWorld) reply(c call, out any) error {
	switch c.Fn {
	case "settings.get":
		return json.Unmarshal([]byte(`{}`), out)
	case "permissions":
		return json.Unmarshal([]byte(w.granted), out)
	case "kv.set":
		w.kv[c.Args["key"].(string)], _ = json.Marshal(c.Args["value"])
	case "kv.delete":
		delete(w.kv, c.Args["key"].(string))
	case "kv.get":
		v, ok := w.kv[c.Args["key"].(string)]
		if !ok {
			return json.Unmarshal([]byte(`{"found":false}`), out)
		}
		b, _ := json.Marshal(map[string]any{"found": true, "value": v})
		return json.Unmarshal(b, out)
	case "entities.list":
		w.listArgs = append(w.listArgs, c.Args)
		if w.listErr != nil {
			return w.listErr
		}
		i := 0
		if cur, ok := c.Args["cursor"].(string); ok {
			fmt.Sscanf(cur, "c%d", &i)
		}
		if i >= len(w.pages) {
			return json.Unmarshal([]byte(`{"items":[],"next_cursor":""}`), out)
		}
		var items []string
		for j, attrs := range w.pages[i] {
			items = append(items, fmt.Sprintf(`{"id":"e%d_%d","name":"Page %d","path":"/blog/p%d","url":"https://example.org/blog/p%d","attributes":%s}`, i, j, j, j, j, attrs))
		}
		next := ""
		if i+1 < len(w.pages) {
			next = fmt.Sprintf("c%d", i+1)
		}
		return json.Unmarshal([]byte(`{"items":[`+strings.Join(items, ",")+`],"next_cursor":"`+next+`"}`), out)
	case "http.fetch":
		u := c.Args["url"].(string)
		w.fetched = append(w.fetched, u)
		w.fetchAt = append(w.fetchAt, w.clock)
		w.clock = w.clock.Add(w.fetchDur)
		st := 200
		if s, ok := w.status[u]; ok {
			st = s
		}
		return json.Unmarshal([]byte(fmt.Sprintf(`{"status":%d}`, st)), out)
	case "entities.get":
		return &sdk.HostError{Code: sdk.ErrNotFound}
	case "redirects.list":
		return json.Unmarshal([]byte(`{"items":[],"total":0}`), out)
	case "db.query", "db.exec":
		if w.dbErr != nil {
			return w.dbErr
		}
		if c.Fn == "db.exec" {
			return nil
		}
		sql := c.sql()
		switch {
		case strings.Contains(sql, "'n', count"):
			return cell(out, `{"n":3}`)
		case strings.Contains(sql, "FROM known_paths WHERE path"):
			return cell(out, `{"known":false}`)
		case strings.Contains(sql, "fail_count, next_check FROM links"):
			kind := c.params()[0].(string)
			var rows []string
			for i, u := range w.due[kind] {
				rows = append(rows, fmt.Sprintf(`{"id":%d,"url":%q,"kind":%q,"fail_count":0}`, i+1, u, kind))
			}
			return cell(out, `[`+strings.Join(rows, ",")+`]`)
		}
		return cell(out, `[]`)
	}
	return nil
}

// updates returns the markResult calls as id -> params.
func (w *scanWorld) updates() map[int64][]any {
	got := map[int64][]any{}
	for _, c := range w.host.calls {
		if c.Fn == "db.exec" && strings.HasPrefix(c.sql(), "UPDATE links SET status") {
			p := c.params()
			got[int64(p[0].(float64))] = p
		}
	}
	return got
}

func TestScanCursorAdvancesAndWraps(t *testing.T) {
	w := newScanWorld(t)
	page := func(href string) string { return `{"content":"<a href=\"` + href + `\">x</a>"}` }
	w.pages = [][]string{{page("/a"), page("/b")}, {page("https://example.com/c")}}

	runScan()
	if got := w.listArgs; len(got) != 2 || got[0]["status"] != "published" || got[0]["limit"] != float64(listLimit) {
		t.Fatalf("list calls %v", got)
	}
	if _, ok := w.kv[cursorKey]; ok {
		t.Fatalf("a finished pass clears the cursor: %s", w.kv[cursorKey])
	}
	if _, ok := w.kv[lastScanKey]; !ok {
		t.Fatal("last scan time must be recorded")
	}
	if _, ok := w.kv[doneKey]; !ok {
		t.Fatal("a finished pass must be recorded")
	}
	// Three entities stored, parents remembered.
	var links, known int
	for _, q := range w.host.sqls() {
		if strings.HasPrefix(q, "INSERT INTO links") {
			links++
		}
		if strings.HasPrefix(q, "INSERT INTO known_paths") {
			known++
		}
	}
	if links != 3 || known != 3 {
		t.Fatalf("links upserts %d known paths %d", links, known)
	}
}

func TestScanCheckpointResumesAfterFailure(t *testing.T) {
	w := newScanWorld(t)
	w.pages = [][]string{{`{}`}, {`{}`}, {`{}`}}
	// One run reads only what the budget allows; here the run is cut after
	// the first page by a failing list call on the second.
	calls := 0
	inner := w.host.reply
	w.host.reply = func(c call, out any) error {
		if c.Fn == "entities.list" {
			calls++
			if calls == 2 {
				return errors.New("host down")
			}
		}
		return inner(c, out)
	}
	runScan()
	var st scanState
	if err := json.Unmarshal(w.kv[cursorKey], &st); err != nil || st.Cursor != "c1" || st.PassTotal != 1 || st.PassStart == "" {
		t.Fatalf("checkpoint %s %v", w.kv[cursorKey], err)
	}
	runScan() // resumes at c1, not at the start
	if len(w.listArgs) != 3 || w.listArgs[1]["cursor"] != "c1" || w.listArgs[2]["cursor"] != "c2" {
		t.Fatalf("the second run must resume at the saved cursor: %v", w.listArgs)
	}
}

func TestScanHalvesListLimitWhenTooBig(t *testing.T) {
	w := newScanWorld(t)
	w.pages = [][]string{{`{}`}}
	inner := w.host.reply
	w.host.reply = func(c call, out any) error {
		if c.Fn == "entities.list" && c.Args["limit"].(float64) > 12 {
			w.listArgs = append(w.listArgs, c.Args)
			return &sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "ask for less"}
		}
		return inner(c, out)
	}
	runScan()
	var limits []float64
	for _, a := range w.listArgs {
		limits = append(limits, a["limit"].(float64))
	}
	if fmt.Sprint(limits) != "[50 25 12]" {
		t.Fatalf("limits %v", limits)
	}
}

func TestScanWithoutDatabaseDoesNothing(t *testing.T) {
	w := newScanWorld(t)
	w.pages = [][]string{{`{}`}}
	w.due[kindOutbound] = []string{"https://example.org/x"}
	w.dbErr = &sdk.HostError{Code: sdk.ErrCapabilityDenied}
	runScan()
	for _, fn := range []string{"entities.list", "http.fetch", "kv.set", "entities.get"} {
		if w.host.count(fn) != 0 {
			t.Fatalf("%s must not be called without db", fn)
		}
	}
	if n := w.host.count("db.query") + w.host.count("db.exec"); n != 1 {
		t.Fatalf("only the probe may run, got %d db calls", n)
	}
}

func TestScanChecksDueLinksAndSchedules(t *testing.T) {
	w := newScanWorld(t)
	w.due[kindInternal] = []string{"/gone"}
	w.due[kindOutbound] = []string{"https://example.org/ok", "https://example.net/missing", "https://example.com/busy"}
	w.status["https://example.net/missing"] = 404
	w.status["https://example.com/busy"] = 503
	runScan()
	var got []string
	for _, c := range w.host.calls {
		if c.Fn == "db.exec" && strings.HasPrefix(c.sql(), "UPDATE links SET status") {
			p := c.params()
			got = append(got, fmt.Sprintf("%v %v %v", p[1], p[2], p[6]))
		}
	}
	// Internal first (no page, rechecked in 7 days), then outbound; each
	// request advances the fake clock by 300 ms.
	want := []string{
		"broken 0 2026-03-08T12:00:00Z",
		"ok 200 2026-03-08T12:00:00Z",
		"broken 404 2026-03-08T12:00:00Z",
		"unknown 503 2026-03-02T12:00:00Z", // 429/503: one day
	}
	if len(got) != len(want) {
		t.Fatalf("results %v", got)
	}
	for i := range want {
		if i == 0 && got[i] != want[i] {
			t.Errorf("result %d: %s want %s", i, got[i], want[i])
		}
		// next_check carries the clock of its own request: same day, later seconds.
		if i > 0 && got[i][:len(got[i])-9] != want[i][:len(want[i])-9] {
			t.Errorf("result %d: %s want %s", i, got[i], want[i])
		}
	}
}

func TestScanOneRequestPerHostPerSecond(t *testing.T) {
	w := newScanWorld(t)
	w.due[kindOutbound] = []string{
		"https://example.org/1", "https://example.org/2", "https://example.org/3",
		"https://example.com/a", "https://EXAMPLE.net/b",
	}
	w.fetchDur = 400 * time.Millisecond
	runScan()
	lastAt := map[string]time.Time{}
	for i, u := range w.fetched {
		h := hostOf(u)
		if prev, ok := lastAt[h]; ok && w.fetchAt[i].Sub(prev) < time.Second {
			t.Errorf("%s requested twice within a second (%v after the previous)", h, w.fetchAt[i].Sub(prev))
		}
		lastAt[h] = w.fetchAt[i]
	}
	// Other hosts go first; example.org's later links wait for time to pass.
	if w.fetched[0] != "https://example.org/1" || w.fetched[1] != "https://example.com/a" || w.fetched[2] != "https://EXAMPLE.net/b" {
		t.Fatalf("order %v", w.fetched)
	}
	if len(w.fetched) != 3 {
		t.Fatalf("no sleeping: skipped links wait for the next run, fetched %v", w.fetched)
	}
}

func TestScanSkippedHostGoesAgainWhenTimeHasPassed(t *testing.T) {
	w := newScanWorld(t)
	w.due[kindOutbound] = []string{"https://example.org/1", "https://example.org/2", "https://example.com/a", "https://example.net/b", "https://example.net/c"}
	w.fetchDur = 600 * time.Millisecond
	runScan()
	// org/1 at 0.0, com at 0.6, net/b at 1.2. In the second pass (1.8 s) org/2 is
	// free again (last used at 0.6 s), net/c (last used at 1.8 s) waits for the next run.
	if len(w.fetched) != 4 || w.fetched[3] != "https://example.org/2" {
		t.Fatalf("fetched %v", w.fetched)
	}
}

func TestScanStopsStartingRequestsWhenBudgetIsUsedUp(t *testing.T) {
	w := newScanWorld(t)
	for i := range 30 {
		w.due[kindOutbound] = append(w.due[kindOutbound], fmt.Sprintf("https://h%d.example.org/", i))
	}
	w.fetchDur = 2 * time.Second
	start := w.clock
	runScan()
	if len(w.fetched) < 3 || len(w.fetched) > 4 {
		t.Fatalf("a 2 s request fits 3 times before the budget runs out, got %d", len(w.fetched))
	}
	if took := w.clock.Sub(start); took >= 10*time.Second || took > budget {
		t.Fatalf("run took %v", took)
	}
	// Every request got a timeout that fits what was left.
	for i, c := range w.host.calls {
		if c.Fn != "http.fetch" {
			continue
		}
		ms := time.Duration(c.Args["timeout_ms"].(float64)) * time.Millisecond
		left := start.Add(budget).Sub(w.fetchAt[indexOfFetch(w.host.calls[:i+1])])
		if ms > 5*time.Second || ms > left-margin+time.Millisecond {
			t.Errorf("timeout %v with %v left", ms, left)
		}
	}
}

func indexOfFetch(calls []call) int {
	n := 0
	for _, c := range calls {
		if c.Fn == "http.fetch" {
			n++
		}
	}
	return n - 1
}

func TestScanTimeoutIsCappedByRemainingBudget(t *testing.T) {
	w := newScanWorld(t)
	w.due[kindOutbound] = []string{"https://a.example.org/", "https://b.example.org/", "https://c.example.org/", "https://d.example.org/"}
	w.fetchDur = 3 * time.Second
	runScan()
	var timeouts []float64
	for _, c := range w.host.calls {
		if c.Fn == "http.fetch" {
			timeouts = append(timeouts, c.Args["timeout_ms"].(float64))
		}
	}
	// 8 s budget, 3 s per request: 8 s left allows the 5 s setting, then 5 s
	// left minus the margin is 4.5 s, then 2 s left gives 1.5 s, then it stops.
	if fmt.Sprint(timeouts) != "[5000 4500 1500]" {
		t.Fatalf("timeouts %v", timeouts)
	}
}

func TestScanFailsOpenAndNeverErrors(t *testing.T) {
	w := newScanWorld(t)
	w.listErr = &sdk.HostError{Code: sdk.ErrUnavailable}
	w.due[kindOutbound] = []string{"https://example.org/x"}
	runScan() // must not panic
	if w.host.count("http.fetch") != 1 {
		t.Fatal("a failing entity list must not stop the link checks")
	}
}

func TestScanTotalDurationUnderTenSeconds(t *testing.T) {
	w := newScanWorld(t)
	for i := range 100 {
		w.due[kindOutbound] = append(w.due[kindOutbound], fmt.Sprintf("https://h%d.example.org/", i))
	}
	for i := range 100 {
		w.due[kindInternal] = append(w.due[kindInternal], fmt.Sprintf("/p%d", i))
	}
	w.fetchDur = 4900 * time.Millisecond
	start := w.clock
	runScan()
	if took := w.clock.Sub(start); took >= 10*time.Second {
		t.Fatalf("took %v", took)
	}
}

func TestScanSkipsOutboundWithoutFetchAnyGrant(t *testing.T) {
	for _, granted := range []string{`{"granted":["http.fetch"]}`, `{"granted":["http.fetch.any"]}`, `{"granted":[]}`} {
		w := newScanWorld(t)
		w.granted = granted
		w.due[kindOutbound] = []string{"https://example.org/x"}
		runScan()
		if w.host.count("http.fetch") != 0 || len(w.updates()) != 0 {
			t.Fatalf("%s: no request and no stored result without the grants", granted)
		}
		if w.host.count("permissions") != 1 {
			t.Fatalf("permissions must be read once per run, got %d", w.host.count("permissions"))
		}
	}
}

func TestScanHeadAndGetStayInsideTheBudget(t *testing.T) {
	w := newScanWorld(t)
	w.due[kindOutbound] = []string{"https://slow.example.org/"}
	w.status["https://slow.example.org/"] = 405
	w.fetchDur = 6900 * time.Millisecond // HEAD ends 0.6 s before the budget
	runScan()
	if len(w.fetched) != 1 {
		t.Fatalf("no GET may start with less than a request's worth left: %v", w.fetched)
	}
	if len(w.updates()) != 0 {
		t.Fatalf("the link must stay due: %v", w.updates())
	}
}

func TestScanKeepsNoPageLinksPendingUntilAPassCompleted(t *testing.T) {
	w := newScanWorld(t)
	// The pass is unfinished: the second list page is read in the next run.
	w.pages = [][]string{{`{}`}, {`{}`}}
	w.due[kindInternal] = []string{"/blog"}
	inner := w.host.reply
	listed := 0
	w.host.reply = func(c call, out any) error {
		if c.Fn == "entities.list" {
			if listed++; listed == 2 {
				return errors.New("host down")
			}
		}
		return inner(c, out)
	}
	runScan()
	if p := w.updates()[1]; p == nil || p[1] != statusPending {
		t.Fatalf("before the pass completed: %v", p)
	}
	if _, ok := w.kv[doneKey]; ok {
		t.Fatal("pass must not be recorded as complete")
	}
	w.host.calls = nil
	runScan() // finishes the pass
	if p := w.updates()[1]; p == nil || p[1] != statusBroken {
		t.Fatalf("after the pass completed: %v", p)
	}
}

func TestScanSkipsAFailingEntityAndContinues(t *testing.T) {
	w := newScanWorld(t)
	w.pages = [][]string{{`{"content":"<a href=\"/a\">x</a>"}`, `{"content":"<a href=\"/b\">x</a>"}`}, {`{}`}}
	inner := w.host.reply
	w.host.reply = func(c call, out any) error {
		if c.Fn == "db.exec" && strings.HasPrefix(c.sql(), "INSERT INTO links") && strings.Contains(fmt.Sprint(c.params()), `"/a"`) {
			return &sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "index row size exceeds"}
		}
		return inner(c, out)
	}
	runScan()
	if len(w.listArgs) != 2 {
		t.Fatalf("the pass must go on past the failing entity: %d list calls", len(w.listArgs))
	}
	if _, ok := w.kv[doneKey]; !ok {
		t.Fatal("the pass must complete")
	}
	var b int
	for _, q := range w.host.calls {
		if q.Fn == "db.exec" && strings.HasPrefix(q.sql(), "INSERT INTO links") && strings.Contains(fmt.Sprint(q.params()), `"/b"`) {
			b++
		}
	}
	if b != 1 {
		t.Fatalf("the entity after the failing one must be stored, got %d", b)
	}
}

func TestScanRestartsPassWhenListKeepsFailing(t *testing.T) {
	w := newScanWorld(t)
	w.pages = [][]string{{`{}`}, {`{}`}}
	w.kv[cursorKey], _ = json.Marshal(scanState{Cursor: "c1", PassStart: "2026-01-01T00:00:00Z"})
	w.listErr = &sdk.HostError{Code: sdk.ErrInvalidArgument, Message: "too big"}
	for i := 1; i <= maxListRetries; i++ {
		runScan()
		var st scanState
		_ = json.Unmarshal(w.kv[cursorKey], &st)
		if i < maxListRetries && (st.Cursor != "c1" || st.Retries != i) {
			t.Fatalf("run %d: %+v", i, st)
		}
		if i == maxListRetries && (st.Cursor != "" || st.Retries != 0 || st.PassStart == "2026-01-01T00:00:00Z") {
			t.Fatalf("the pass must restart after %d runs: %+v", maxListRetries, st)
		}
	}
	if _, ok := w.kv[doneKey]; ok {
		t.Fatal("a restart is not a completed pass")
	}
}
