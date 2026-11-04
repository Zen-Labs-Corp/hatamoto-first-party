package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
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
	psiFail  string // when set, PSI answers this error
	cruxCode int
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
		if w.psiFail != "" {
			return errors.New(w.psiFail)
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
	case strings.HasPrefix(sql, "INSERT INTO origin_summary"):
		w.origins[args[1].(string)] = true
	case strings.HasPrefix(sql, "INSERT INTO metrics") && strings.Contains(sql, "field_status"):
		w.fields[[2]string{args[0].(string), args[2].(string)}] = fieldRow{args[9].(string)}
	case strings.HasPrefix(sql, "INSERT INTO metrics"):
		w.labs[[2]string{args[0].(string), args[2].(string)}] = args[4].(string)
	case strings.Contains(sql, "INSERT INTO progress") && strings.Contains(sql, "lab_state"):
		p := w.prog(args[1].(string), args[2].(string))
		p.CruxDone, p.LabState = true, args[3].(string)
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

func TestFinishedDayMakesNoFetch(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	for range 5 {
		if _, err := measureStep(); err != nil {
			t.Fatal(err)
		}
	}
	n := len(w.fetches)
	if n != 3 { // origin, crux, psi
		t.Fatalf("fetches = %v", w.fetches)
	}
	if err := runCron(); err != nil {
		t.Fatal(err)
	}
	if len(w.fetches) != n {
		t.Fatalf("finished day fetched again: %v", w.fetches)
	}
	if w.deletes != 3 {
		t.Fatalf("retention ran %d deletes", w.deletes)
	}
}

func TestLabTimeoutKeepsFieldDataAndRetriesTwice(t *testing.T) {
	w := newWorld(t, `{"api_key":"`+testKey+`","public_url":"https://s.example","strategy":"mobile","urls":["/a"]}`)
	w.psiFail = "context deadline exceeded for " + testKey
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
	w.psiFail = "dial tcp: lookup failed for key=" + testKey
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
