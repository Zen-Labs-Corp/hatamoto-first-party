package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// world is a fake host: settings, entities, an in-memory db behind the SQL the
// store issues, a scripted http.fetch and a clock that moves with every fetch.
type world struct {
	t        *testing.T
	clock    time.Time
	settings string
	progress map[[2]string]*progressRow
	origins  map[string]bool
	fields   map[[2]string]fieldRow
	labs     map[[2]string]string
	deletes  int
	logs     []string
	fetches  []string // "crux:<url>", "origin:<url>", "psi:<url>"
	cruxCost time.Duration
	psiCost  time.Duration
	psiFail  error // when set, PSI answers this error
	cruxCode int
	dayDone  bool           // the "day complete" marker row
	calls    map[string]int // host calls by function
}

type fieldRow struct{ status string }

func newWorld(t *testing.T, settings string) *world {
	w := &world{
		t: t, clock: time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC), settings: settings,
		progress: map[[2]string]*progressRow{}, origins: map[string]bool{},
		fields: map[[2]string]fieldRow{}, labs: map[[2]string]string{},
		cruxCost: 500 * time.Millisecond, psiCost: 2 * time.Second, cruxCode: 200,
	}
	now = func() time.Time { return w.clock }
	budgetEnd = time.Time{}
	callHost = w.host
	t.Cleanup(func() { now = time.Now; budgetEnd = time.Time{} })
	return w
}

func (w *world) count(prefix string) (n int) {
	for _, f := range w.fetches {
		if strings.HasPrefix(f, prefix) {
			n++
		}
	}
	return
}

func (w *world) host(fn string, args, out any) error {
	if w.calls == nil {
		w.calls = map[string]int{}
	}
	w.calls[fn]++
	switch fn {
	case "settings.get":
		return jsonInto(w.settings, out)
	case "entities.list":
		return jsonInto(`{"items":[]}`, out)
	case "log":
		w.logs = append(w.logs, args.(map[string]string)["message"])
		return nil
	case "http.fetch":
		return w.fetch(args.(map[string]any), out)
	case "db.query":
		return w.query(args.(map[string]any)["sql"].(string), args.(map[string]any)["args"].([]any), out)
	case "db.exec":
		return w.exec(args.(map[string]any)["sql"].(string), args.(map[string]any)["args"].([]any))
	}
	w.t.Fatalf("unexpected host call %q", fn)
	return nil
}

func (w *world) fetch(a map[string]any, out any) error {
	u := a["url"].(string)
	var res fetched
	switch {
	case strings.Contains(u, "chromeuxreport"):
		var body map[string]string
		_ = json.Unmarshal([]byte(a["body"].(string)), &body)
		kind, target := "crux", body["url"]
		if target == "" {
			kind, target = "origin", body["origin"]
		}
		w.fetches = append(w.fetches, kind+":"+target)
		w.clock = w.clock.Add(w.cruxCost)
		res = fetched{Status: w.cruxCode, Body: `{"record":{"metrics":{"largest_contentful_paint":{"percentiles":{"p75":2300}}}}}`}
	default:
		w.fetches = append(w.fetches, "psi:"+u)
		w.clock = w.clock.Add(w.psiCost)
		if w.psiFail != nil {
			return w.psiFail
		}
		res = fetched{Status: 200, Body: `{"lighthouseResult":{"categories":{"performance":{"score":0.9}}}}`}
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

func (w *world) query(sql string, args []any, out any) error {
	var cell any
	switch {
	case strings.Contains(sql, "FROM origin_summary"):
		list := []string{}
		for s := range w.origins {
			list = append(list, s)
		}
		cell = list
	case strings.Contains(sql, "url = '' AND strategy = 'day'"):
		list := []string{}
		if w.dayDone {
			list = append(list, "day")
		}
		cell = list
	case strings.Contains(sql, "FROM progress"):
		rows := []progressRow{}
		for _, r := range w.progress {
			rows = append(rows, *r)
		}
		cell = rows
	default:
		w.t.Fatalf("unexpected query %.60s", sql)
	}
	b, _ := json.Marshal(cell)
	s, _ := json.Marshal(string(b))
	return json.Unmarshal([]byte(`{"rows":[[`+string(s)+`]]}`), out)
}

func (w *world) prog(url, strategy string) *progressRow {
	k := [2]string{url, strategy}
	if w.progress[k] == nil {
		w.progress[k] = &progressRow{URL: url, Strategy: strategy, LabState: labPending}
	}
	return w.progress[k]
}

func (w *world) exec(sql string, args []any) error {
	switch {
	case strings.Contains(sql, "INSERT INTO progress") && strings.Contains(sql, "'day'"):
		w.dayDone = true
	case strings.HasPrefix(sql, "INSERT INTO origin_summary"):
		w.origins[args[1].(string)] = true
	case strings.HasPrefix(sql, "INSERT INTO metrics") && strings.Contains(sql, "field_status"):
		w.fields[[2]string{args[0].(string), args[2].(string)}] = fieldRow{args[9].(string)}
	case strings.HasPrefix(sql, "INSERT INTO metrics"):
		k := [2]string{args[0].(string), args[2].(string)}
		if strings.Contains(sql, "lab_status <> 'ok'") && w.labs[k] == labOK && args[4].(string) != labOK {
			break // the guard keeps the finished score
		}
		w.labs[k] = args[4].(string)
	case strings.Contains(sql, "INSERT INTO progress") && strings.Contains(sql, "lab_state"):
		p := w.prog(args[1].(string), args[2].(string))
		p.CruxDone = true
		if !(p.LabState == labOK && args[3].(string) != labOK && strings.Contains(sql, "progress.lab_state = 'ok'")) {
			p.LabState = args[3].(string)
		}
		p.LabAttempts++
	case strings.Contains(sql, "INSERT INTO progress"):
		w.prog(args[1].(string), args[2].(string)).CruxDone = true
	case strings.HasPrefix(sql, "DELETE"):
		w.deletes++
	default:
		w.t.Fatalf("unexpected exec %.60s", sql)
	}
	return nil
}

const baseSettings = `{"api_key":"` + testKey + `","public_url":"https://s.example","strategy":"mobile","urls":["/a","/b","/c","/d","/e","/f"]}`

func TestCallStopsAtBudgetWithOnePSI(t *testing.T) {
	w := newWorld(t, baseSettings)
	w.cruxCost = 100 * time.Millisecond
	start := w.clock
	for range 3 {
		if _, err := measureStep(); err != nil {
			t.Fatal(err)
		}
		if spent := w.clock.Sub(start); spent > budget+w.psiCost {
			t.Fatalf("call ran %v", spent)
		}
		start = w.clock
	}
	if w.count("psi:") > 3 {
		t.Fatalf("more than one PSI per call: %v", w.fetches)
	}
	// one call: all cruxes fit, then exactly one PSI
	w2 := newWorld(t, baseSettings)
	w2.cruxCost = 100 * time.Millisecond
	if _, err := measureStep(); err != nil {
		t.Fatal(err)
	}
	if w2.count("psi:") != 1 || w2.count("crux:") != 6 || w2.count("origin:") != 1 {
		t.Fatalf("fetches = %v", w2.fetches)
	}
}

func TestSlowFieldLookupsStopAtBudget(t *testing.T) {
	w := newWorld(t, baseSettings)
	w.cruxCost = 1500 * time.Millisecond
	if _, err := measureStep(); err != nil {
		t.Fatal(err)
	}
	if spent := w.clock.Sub(time.Date(2026, 11, 4, 6, 0, 0, 0, time.UTC)); spent > budget+w.cruxCost {
		t.Fatalf("spent %v", spent)
	}
	if w.count("psi:") != 0 || len(w.fetches) >= 8 {
		t.Fatalf("fetches = %v", w.fetches)
	}
}

func TestNextCallResumesFromCheckpoint(t *testing.T) {
	w := newWorld(t, baseSettings)
	w.cruxCost = 1500 * time.Millisecond
	for range 3 {
		if _, err := measureStep(); err != nil {
			t.Fatal(err)
		}
	}
	seen := map[string]bool{}
	for _, f := range w.fetches {
		if seen[f] {
			t.Fatalf("%s fetched twice: %v", f, w.fetches)
		}
		seen[f] = true
	}
	if len(w.progress) < 6 {
		t.Fatalf("checkpoint covers %d pages", len(w.progress))
	}
}

func TestFinishedDayMakesNoFetchAndRunsRetentionOnce(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	for range 3 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.fetches) != 3 || !w.dayDone || w.deletes != 3 { // origin, crux, psi
		t.Fatalf("fetches = %v, done %v, deletes %d", w.fetches, w.dayDone, w.deletes)
	}
	for range 5 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.fetches) != 3 || w.deletes != 3 {
		t.Fatalf("finished day did work again: fetches %v, deletes %d, calls %v", w.fetches, w.deletes, w.calls)
	}
}

func TestSettingsChangedAfterTheDayFinishedArePickedUp(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	for range 3 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	if len(w.fetches) != 3 || w.deletes != 3 {
		t.Fatalf("fetches = %v, deletes %d", w.fetches, w.deletes)
	}
	w.settings = `{"api_key":"` + testKey + `","public_url":"https://s.example","strategy":"both","urls":["/a","/b"]}`
	for range 12 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	// desktop origin, then 3 new page/device pairs, crux and psi each
	if w.count("origin:") != 2 || w.count("crux:") != 4 || w.count("psi:") != 4 {
		t.Fatalf("new settings not measured: %v", w.fetches)
	}
	if w.deletes != 3 {
		t.Fatalf("retention ran again: %d", w.deletes)
	}
}

func TestLabTimeoutKeepsFieldDataAndRetriesTwice(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	w.psiFail = &sdk.HostError{Code: sdk.ErrTimeout, Message: "call timed out " + testKey}
	for range 8 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	if w.fields[[2]string{"/a", "mobile"}].status != fieldOK {
		t.Fatalf("field data lost: %+v", w.fields)
	}
	if w.labs[[2]string{"/a", "mobile"}] != labTimeout {
		t.Fatalf("labs = %v", w.labs)
	}
	if w.count("psi:") != 3 {
		t.Fatalf("lab ran %d times, want the run and two retries: %v", w.count("psi:"), w.fetches)
	}
}

func TestNoFieldDataIsNotAnError(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	w.cruxCode = 404
	if _, err := measureStep(); err != nil {
		t.Fatal(err)
	}
	if got := w.fields[[2]string{"/a", "mobile"}].status; got != fieldNone {
		t.Fatalf("field status = %q", got)
	}
	if len(w.logs) != 0 {
		t.Fatalf("logs = %v", w.logs)
	}
}

func TestLogsNeverHoldTheKey(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	w.psiFail = errors.New("dial tcp: lookup failed for key=" + testKey)
	w.cruxCode = 403
	for range 4 {
		_ = runCron()
	}
	if len(w.logs) == 0 {
		t.Fatal("failures were not logged")
	}
	for _, l := range w.logs {
		if strings.Contains(l, testKey) {
			t.Fatalf("key in log: %s", l)
		}
	}
}

func TestMissingKeyOrOriginFailsOpen(t *testing.T) {
	newWorld(t, `{"public_url":"https://s.example"}`)
	if err := runCron(); err != nil {
		t.Fatal(err)
	}
	w := newWorld(t, `{"api_key":"k","urls":["/a"]}`)
	if err := runCron(); err != nil || len(w.fetches) != 0 {
		t.Fatalf("%v %v", err, w.fetches)
	}
}

func TestMeasureNowTimeoutKeepsTodaysLabScore(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	for range 4 {
		if err := runCron(); err != nil {
			t.Fatal(err)
		}
	}
	k := [2]string{"/a", "mobile"}
	if w.labs[k] != labOK {
		t.Fatalf("labs = %v", w.labs)
	}
	w.psiFail = &sdk.HostError{Code: sdk.ErrTimeout, Message: "call timed out"}
	p, err := loadPlan()
	if err != nil {
		t.Fatal(err)
	}
	defer startBudget()()
	if err := measureOne(p, "/a", "mobile"); err != nil {
		t.Fatal(err)
	}
	if w.labs[k] != labOK || w.progress[k].LabState != labOK {
		t.Fatalf("timeout replaced the score: %v %+v", w.labs, w.progress[k])
	}
}
