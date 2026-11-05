package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const e2eKey = "E2E-SECRET-KEY"

// e2eHost is one fake host for a whole run. It enforces the run phases the way
// the real host does: buffer reads, site reads, the slug check and the
// changeset only in transform; http.fetch and buffer writes only in fetch.
type e2eHost struct {
	t        *testing.T
	phase    string
	params   string
	pages    map[string][]entity
	next     map[string]string
	groups   map[string][]string
	failAt   int    // answer the n-th provider call (1-based) with 403; 0 = never
	failBody string // body of that 403

	buffer     map[string][]byte
	checkpoint string
	finished   bool
	fails      []string
	progress   []string
	summary    json.RawMessage
	upserts    []sdk.EntityUpsert
	fetches    int
	stepFetch  int
	urls       []string
	bodies     []string
}

func newE2EHost(t *testing.T) *e2eHost {
	h := &e2eHost{
		t:      t,
		phase:  sdk.RunPhaseFetch,
		params: `{"locale":"de","schema":"page","only_missing":true}`,
		pages:  map[string][]entity{},
		next:   map[string]string{},
		groups: map[string][]string{},
		buffer: map[string][]byte{},
	}
	callHost = h.call
	budgetEnd = time.Time{}
	t.Cleanup(func() { callHost = nil; budgetEnd = time.Time{} })
	return h
}

// e2eMarked wraps every text node in [de]...[/de] and keeps the tags.
func e2eMarked(s string) string {
	var b strings.Builder
	for _, seg := range scanHTML(s) {
		if seg.markup || strings.TrimSpace(seg.text) == "" {
			b.WriteString(seg.text)
		} else {
			b.WriteString("[de]" + seg.text + "[/de]")
		}
	}
	return b.String()
}

func (h *e2eHost) denied(fn string, phases ...string) error {
	for _, p := range phases {
		if h.phase == p {
			return nil
		}
	}
	h.t.Logf("%s refused in the %s phase", fn, h.phase)
	return &sdk.HostError{Code: sdk.ErrPhaseDenied}
}

func (h *e2eHost) call(fn string, args, out any) error {
	var res any
	switch fn {
	case sdk.FnRunInfo:
		res = sdk.RunInfo{RunID: "r1", Name: "translate", Phase: h.phase, Cursor: h.checkpoint,
			Params: json.RawMessage(h.params), Hosts: []string{"api.deepl.com"}}
	case "settings.get":
		res = map[string]any{"provider": "deepl", "api_key": e2eKey,
			"glossary": []map[string]string{{"source": "Hatamoto", "target": ""}}}
	case sdk.FnEntitiesList:
		a := args.(map[string]any)
		cur, _ := a["cursor"].(string)
		items, ok := h.pages[cur]
		if !ok {
			h.t.Fatalf("unknown list cursor %q", cur)
		}
		res = map[string]any{"items": items, "next_cursor": h.next[cur]}
	case sdk.FnEntitiesTranslations:
		var items []map[string]string
		for _, l := range h.groups[args.(map[string]string)["id"]] {
			items = append(items, map[string]string{"id": "x", "locale": l})
		}
		res = items
	case "http.fetch":
		if err := h.denied(fn, sdk.RunPhaseFetch); err != nil {
			return err
		}
		r := args.(fetchReq)
		h.fetches++
		h.stepFetch++
		h.urls = append(h.urls, r.URL)
		h.bodies = append(h.bodies, r.Body)
		if h.failAt > 0 && h.fetches == h.failAt {
			res = fetched{Status: 403, Body: h.failBody}
			break
		}
		var in struct {
			Text []string `json:"text"`
		}
		if err := json.Unmarshal([]byte(r.Body), &in); err != nil {
			h.t.Fatal(err)
		}
		var tr []map[string]string
		for _, s := range in.Text {
			tr = append(tr, map[string]string{"text": e2eMarked(s)})
		}
		b, _ := json.Marshal(map[string]any{"translations": tr})
		res = fetched{Status: 200, Body: string(b)}
	case sdk.FnBufferPut:
		if err := h.denied(fn, sdk.RunPhaseFetch); err != nil {
			return err
		}
		a := args.(sdk.BufferPutArgs)
		v, _ := base64.StdEncoding.DecodeString(a.ValueB64)
		h.buffer[a.Key] = v
	case sdk.FnBufferGet:
		if err := h.denied(fn, sdk.RunPhaseTransform); err != nil {
			return err
		}
		v, ok := h.buffer[args.(sdk.BufferGetArgs).Key]
		res = sdk.BufferValue{Found: ok, ValueB64: base64.StdEncoding.EncodeToString(v)}
	case sdk.FnBufferList:
		if err := h.denied(fn, sdk.RunPhaseTransform); err != nil {
			return err
		}
		a := args.(sdk.BufferListArgs)
		var keys []string
		for k := range h.buffer {
			if strings.HasPrefix(k, a.Prefix) && k > a.After {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		next := ""
		if a.Limit > 0 && len(keys) > a.Limit {
			keys = keys[:a.Limit]
			next = keys[len(keys)-1]
		}
		res = sdk.BufferKeys{Keys: keys, Next: next}
	case sdk.FnSiteInfo:
		if err := h.denied(fn, sdk.RunPhaseTransform); err != nil {
			return err
		}
		res = sdk.SiteInfo{Title: "Site", Language: "en", Locales: []string{"en", "de"}}
	case sdk.FnEntitiesSlugTaken:
		if err := h.denied(fn, sdk.RunPhaseTransform); err != nil {
			return err
		}
		res = sdk.SlugTaken{}
	case sdk.FnChangesetEntityUpsert:
		if err := h.denied(fn, sdk.RunPhaseTransform); err != nil {
			return err
		}
		up := args.(sdk.EntityUpsert)
		h.upserts = append(h.upserts, up)
		res = sdk.UpsertResult{SourceKey: up.SourceKey, Created: true}
	case sdk.FnRunCheckpoint:
		h.checkpoint = args.(sdk.RunCheckpointArgs).Cursor
	case sdk.FnRunProgress:
		h.progress = append(h.progress, args.(sdk.RunProgressArgs).Message)
	case sdk.FnRunFinish:
		h.finished = true
		h.summary = args.(sdk.RunFinishArgs).Summary
	case sdk.FnRunFail:
		h.fails = append(h.fails, args.(sdk.RunFailArgs).Reason)
	default:
		h.t.Fatalf("unexpected host call %q", fn)
	}
	if out == nil {
		return nil
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

// phaseSteps runs the current phase to its end (or the run's failure) and
// returns the number of steps. Every step may call the provider at most once.
func (h *e2eHost) phaseSteps() int {
	h.t.Helper()
	h.finished, h.checkpoint = false, ""
	for n := 1; n <= 60; n++ {
		h.stepFetch = 0
		in, _ := json.Marshal(sdk.RunStep{RunID: "r1", Phase: h.phase, Cursor: h.checkpoint})
		if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
			h.t.Fatalf("step %d returned an error: %v", n, err)
		}
		if h.stepFetch > 1 {
			h.t.Fatalf("step %d made %d provider calls", n, h.stepFetch)
		}
		if len(h.fails) > 0 {
			return n
		}
		if h.finished {
			return n
		}
	}
	h.t.Fatal("the phase did not finish")
	return 0
}

// runAll drives fetch, then transform, like the host does.
func (h *e2eHost) runAll() {
	h.t.Helper()
	h.phase = sdk.RunPhaseFetch
	h.phaseSteps()
	if len(h.fails) > 0 {
		return
	}
	h.phase = sdk.RunPhaseTransform
	h.phaseSteps()
}

// e2eWorld has 45 pages over three list pages: p05 is built with the page
// builder and p10 already has a German translation.
func e2eWorld(t *testing.T) *e2eHost {
	h := newE2EHost(t)
	var all []entity
	for i := 1; i <= 45; i++ {
		id := fmt.Sprintf("p%02d", i)
		attrs := fmt.Sprintf(`{"content":"<h2>Hatamoto page %d</h2><p>Body <a href=\"/x\">link</a> %d</p>","layout":"wide"}`, i, i)
		group := ""
		switch i {
		case 5:
			attrs = `{"page_doc":{"blocks":[1]}}`
		case 10:
			group = "g10"
		}
		all = append(all, ent(id, "en", group, attrs))
	}
	h.pages[""], h.next[""] = all[:20], "c2"
	h.pages["c2"], h.next["c2"] = all[20:40], "c3"
	h.pages["c3"] = all[40:]
	h.groups["p10"] = []string{"en", "de"}
	return h
}

func TestRunHappyPath(t *testing.T) {
	h := e2eWorld(t)
	h.runAll()
	if len(h.fails) > 0 || !h.finished {
		t.Fatalf("fails=%v finished=%v", h.fails, h.finished)
	}
	if len(h.upserts) != 43 {
		t.Fatalf("%d upserts, want 43", len(h.upserts))
	}
	seenSlug := map[string]bool{}
	for _, u := range h.upserts {
		id := u.TranslationOf
		if id == "p05" || id == "p10" || u.Locale != "de" || u.SourceKey != "tr:de:"+id || u.Schema != "page" {
			t.Fatalf("upsert %+v", u)
		}
		if seenSlug[u.Slug] || u.Slug == "" {
			t.Fatalf("slug %q empty or repeated", u.Slug)
		}
		seenSlug[u.Slug] = true
		var attrs map[string]string
		if err := json.Unmarshal(u.Attributes, &attrs); err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(attrs["content"], `<a href="/x">[de]link[/de]</a>`) || attrs["layout"] != "wide" {
			t.Fatalf("attributes %v", attrs)
		}
		// the glossary term survives and no protection wrapper is left behind
		if !strings.Contains(attrs["content"], "Hatamoto") || strings.Contains(attrs["content"], "data-hm-g") {
			t.Fatalf("glossary handling: %s", attrs["content"])
		}
	}
	var sum struct {
		Locale  string         `json:"locale"`
		Schema  string         `json:"schema"`
		Staged  int            `json:"staged"`
		Skipped map[string]int `json:"skipped"`
	}
	if err := json.Unmarshal(h.summary, &sum); err != nil {
		t.Fatal(err)
	}
	if sum.Locale != "de" || sum.Schema != "page" || sum.Staged != 43 || sum.Skipped["builder"] != 1 || sum.Skipped["has_translation"] != 1 ||
		sum.Skipped["conflict"] != 0 || sum.Skipped["too_large"] != 0 {
		t.Fatalf("summary %s", h.summary)
	}
}

func TestRunStepsResumeFromTheCheckpoint(t *testing.T) {
	h := e2eWorld(t)
	h.phase = sdk.RunPhaseFetch
	steps := h.phaseSteps()
	if steps < 3 {
		t.Fatalf("%d fetch steps for three list pages", steps)
	}
	// no entity twice, none missing
	seen := map[string]int{}
	for k, v := range h.buffer {
		if !strings.HasPrefix(k, "src/") {
			continue
		}
		var r record
		if err := json.Unmarshal(v, &r); err != nil {
			t.Fatal(err)
		}
		seen[r.ID]++
	}
	if len(seen) != 43 {
		t.Fatalf("%d records", len(seen))
	}
	for id, n := range seen {
		if n != 1 {
			t.Fatalf("%s stored %d times", id, n)
		}
	}
}

func TestRunProviderRejectionFailsBeforeAnyUpsert(t *testing.T) {
	h := e2eWorld(t)
	h.failAt, h.failBody = 2, `{"message":"Authorization failure, check `+e2eKey+`"}`
	h.runAll()
	if len(h.fails) != 1 || !strings.HasPrefix(h.fails[0], "DeepL rejected the key") {
		t.Fatalf("fails=%v", h.fails)
	}
	if len(h.upserts) != 0 || h.phase != sdk.RunPhaseFetch || h.fetches != 2 {
		t.Fatalf("upserts=%d phase=%s fetches=%d", len(h.upserts), h.phase, h.fetches)
	}
}

func TestRunKeyAppearsNowhereItShouldNot(t *testing.T) {
	check := func(name string, h *e2eHost) {
		t.Helper()
		var seen []string
		seen = append(seen, h.urls...)
		seen = append(seen, h.bodies...)
		seen = append(seen, h.fails...)
		seen = append(seen, h.progress...)
		seen = append(seen, h.checkpoint, string(h.summary))
		for _, v := range h.buffer {
			seen = append(seen, string(v))
		}
		for _, u := range h.upserts {
			b, _ := json.Marshal(u)
			seen = append(seen, string(b))
		}
		if strings.Contains(strings.Join(seen, "\n"), e2eKey) {
			t.Fatalf("%s: the key leaked", name)
		}
	}
	ok := e2eWorld(t)
	ok.runAll()
	check("completed run", ok)
	bad := e2eWorld(t)
	bad.failAt, bad.failBody = 1, `{"message":"bad key `+e2eKey+`"}`
	bad.runAll()
	if len(bad.fails) != 1 {
		t.Fatalf("fails=%v", bad.fails)
	}
	check("failed run", bad)
}

func TestRunRerunGivesTheSameSourceKeys(t *testing.T) {
	keys := func() []string {
		h := e2eWorld(t)
		h.runAll()
		var ks []string
		for _, u := range h.upserts {
			ks = append(ks, u.SourceKey+"|"+u.Slug)
		}
		return ks
	}
	a, b := keys(), keys()
	if len(a) != 43 || strings.Join(a, ",") != strings.Join(b, ",") {
		t.Fatalf("%d vs %d keys, or they differ", len(a), len(b))
	}
}
