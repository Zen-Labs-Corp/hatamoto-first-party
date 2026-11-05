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

const fetchTestKey = "FETCH-SECRET-KEY"

// runWorld is a fake host for the fetch phase: paged entities, a fake DeepL
// that upper-cases text nodes, the run buffer and the run.* recorders. Host
// functions that are denied in the fetch phase fail the test.
type runWorld struct {
	t        *testing.T
	settings string
	params   string
	hosts    []string
	pages    map[string][]entity
	next     map[string]string
	groups   map[string][]string
	status   int    // DeepL status; 0 = 200
	errBody  string // DeepL body when status != 200

	buffer      map[string][]byte
	fetches     int
	fetchReqs   []fetchReq
	checkpoint  string
	progress    []string
	finished    bool
	failReasons []string
}

func newRunWorld(t *testing.T) *runWorld {
	w := &runWorld{
		t:        t,
		settings: fmt.Sprintf(`{"provider":"deepl","api_key":%q}`, fetchTestKey),
		params:   `{"locale":"de","schema":"page"}`,
		hosts:    []string{"api.deepl.com"},
		pages:    map[string][]entity{},
		next:     map[string]string{},
		groups:   map[string][]string{},
		buffer:   map[string][]byte{},
	}
	callHost = w.call
	budgetEnd = time.Time{}
	t.Cleanup(func() { callHost = nil; budgetEnd = time.Time{} })
	return w
}

func (w *runWorld) call(fn string, args, out any) error {
	var res any
	switch fn {
	case sdk.FnRunInfo:
		res = sdk.RunInfo{RunID: "r1", Name: "translate", Phase: "fetch", Params: json.RawMessage(w.params), Hosts: w.hosts}
	case "settings.get":
		res = json.RawMessage(w.settings)
	case sdk.FnEntitiesList:
		a := args.(map[string]any)
		cur, _ := a["cursor"].(string)
		items, ok := w.pages[cur]
		if !ok {
			w.t.Fatalf("unknown list cursor %q", cur)
		}
		res = map[string]any{"items": items, "next_cursor": w.next[cur]}
	case sdk.FnEntitiesTranslations:
		var items []map[string]string
		for _, l := range w.groups[args.(map[string]string)["id"]] {
			items = append(items, map[string]string{"id": "x", "locale": l})
		}
		res = items
	case "http.fetch":
		w.fetches++
		r := args.(fetchReq)
		w.fetchReqs = append(w.fetchReqs, r)
		if w.status != 0 && w.status != 200 {
			res = fetched{Status: w.status, Body: w.errBody}
			break
		}
		var in struct {
			Text []string `json:"text"`
		}
		if err := json.Unmarshal([]byte(r.Body), &in); err != nil {
			w.t.Fatal(err)
		}
		var tr []map[string]string
		for _, s := range fakeTranslate(in.Text) {
			tr = append(tr, map[string]string{"text": s})
		}
		b, _ := json.Marshal(map[string]any{"translations": tr})
		res = fetched{Status: 200, Body: string(b)}
	case sdk.FnBufferPut:
		a := args.(sdk.BufferPutArgs)
		v, err := base64.StdEncoding.DecodeString(a.ValueB64)
		if err != nil {
			w.t.Fatal(err)
		}
		w.buffer[a.Key] = v
	case sdk.FnRunCheckpoint:
		w.checkpoint = args.(sdk.RunCheckpointArgs).Cursor
	case sdk.FnRunProgress:
		w.progress = append(w.progress, args.(sdk.RunProgressArgs).Message)
	case sdk.FnRunFinish:
		w.finished = true
	case sdk.FnRunFail:
		w.failReasons = append(w.failReasons, args.(sdk.RunFailArgs).Reason)
	case sdk.FnBufferGet, sdk.FnBufferList, sdk.FnSiteInfo, sdk.FnEntitiesSlugTaken, sdk.FnChangesetEntityUpsert:
		return &sdk.HostError{Code: sdk.ErrPhaseDenied}
	default:
		w.t.Fatalf("unexpected host call %q", fn)
	}
	if out == nil {
		return nil
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

// step runs one fetch step from the stored checkpoint.
func (w *runWorld) step() {
	w.t.Helper()
	in, _ := json.Marshal(sdk.RunStep{RunID: "r1", Phase: sdk.RunPhaseFetch, Cursor: w.checkpoint})
	if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
		w.t.Fatalf("step returned an error: %v", err)
	}
}

// run steps until the phase finishes or the run fails; it returns the number
// of steps and checks that no step made more than one provider call.
func (w *runWorld) run() int {
	w.t.Helper()
	for n := 1; n <= 30; n++ {
		before := w.fetches
		w.step()
		if w.fetches-before > 1 {
			w.t.Fatalf("step %d made %d provider calls", n, w.fetches-before)
		}
		if w.finished || len(w.failReasons) > 0 {
			return n
		}
	}
	w.t.Fatal("the phase did not finish")
	return 0
}

func (w *runWorld) records() (recs []record) {
	var keys []string
	for k := range w.buffer {
		if strings.HasPrefix(k, "src/") {
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	for _, k := range keys {
		var r record
		if err := json.Unmarshal(w.buffer[k], &r); err != nil {
			w.t.Fatal(err)
		}
		recs = append(recs, r)
	}
	return recs
}

func (w *runWorld) stats() (total fetchStat) {
	total.Skipped = map[string]int{}
	for k, v := range w.buffer {
		if !strings.HasPrefix(k, "stat/") {
			continue
		}
		var s fetchStat
		if err := json.Unmarshal(v, &s); err != nil {
			w.t.Fatal(err)
		}
		total.Translated += s.Translated
		total.GlossaryLost += s.GlossaryLost
		for r, n := range s.Skipped {
			total.Skipped[r] += n
		}
	}
	return total
}

// twoPageWorld has 7 entities over two list pages: one builder-only, one
// already in German, one with a German translation.
func twoPageWorld(t *testing.T) *runWorld {
	w := newRunWorld(t)
	body := `{"content":"<p>text of %s</p>","extra":{"k":1}}`
	var p1, p2 []entity
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("e%d", i)
		p1 = append(p1, ent(id, "en", "g"+id, fmt.Sprintf(body, id)))
	}
	p1 = append(p1, ent("builder", "en", "", `{"page_doc":{"x":1}}`))
	p1 = append(p1, ent("german", "de", "", `{"content":"<p>hallo</p>"}`))
	p2 = append(p2, ent("e7", "en", "ge7", fmt.Sprintf(body, "e7")))
	p2 = append(p2, ent("e8", "en", "ge8", fmt.Sprintf(body, "e8")))
	w.pages[""], w.next[""] = p1, "p2"
	w.pages["p2"] = p2
	return w
}

func TestFetchRunAcrossPages(t *testing.T) {
	w := twoPageWorld(t)
	w.run()
	if !w.finished || len(w.failReasons) > 0 {
		t.Fatalf("finished=%v fail=%v", w.finished, w.failReasons)
	}
	recs := w.records()
	var got []string
	for _, r := range recs {
		got = append(got, r.ID)
		if r.TargetLocale != "de" || r.SourceLocale != "en" || r.SchemaSlug != "page" {
			t.Fatalf("record %+v", r)
		}
	}
	if strings.Join(got, ",") != "e1,e2,e3,e4,e7,e8" {
		t.Fatalf("records %v", got)
	}
	if recs[0].T.Content != "<p>TEXT OF E1</p>" || string(recs[0].Attributes["extra"]) != `{"k":1}` {
		t.Fatalf("record %+v", recs[0])
	}
	st := w.stats()
	if st.Translated != 6 || st.Skipped["builder"] != 1 || st.Skipped["same_locale"] != 1 {
		t.Fatalf("stats %+v", st)
	}
	for k := range w.buffer {
		if strings.HasPrefix(k, "src/") && !strings.HasPrefix(k, "src/0000000") {
			t.Fatalf("odd key %q", k)
		}
	}
	if w.fetchReqs[0].URL != "https://api.deepl.com/v2/translate" {
		t.Fatal(w.fetchReqs[0].URL)
	}
}

func TestFetchOnlyMissingSkipsTranslatedGroup(t *testing.T) {
	w := twoPageWorld(t)
	w.params = `{"locale":"de","schema":"page","only_missing":true}`
	w.groups["e2"] = []string{"en", "DE"}
	w.groups["e3"] = []string{"en", "fr"}
	w.run()
	var got []string
	for _, r := range w.records() {
		got = append(got, r.ID)
	}
	if strings.Join(got, ",") != "e1,e3,e4,e7,e8" {
		t.Fatalf("records %v", got)
	}
	if w.stats().Skipped["has_translation"] != 1 {
		t.Fatalf("stats %+v", w.stats())
	}
}

// bigWorld has entities whose bodies are so large that only two fit in one
// provider request.
func bigWorld(t *testing.T, n int) *runWorld {
	w := newRunWorld(t)
	var items []entity
	for i := 1; i <= n; i++ {
		id := fmt.Sprintf("b%d", i)
		content := "<p>" + strings.Repeat("word ", 9000) + id + "</p>"
		items = append(items, ent(id, "en", "", fmt.Sprintf(`{"content":%q}`, content)))
		if i == 2 {
			items = append(items, ent("skip", "de", "", ""))
		}
	}
	w.pages[""] = items
	return w
}

func TestFetchBatchCutResumesWithoutGapsOrDuplicates(t *testing.T) {
	w := bigWorld(t, 5)
	steps := w.run()
	if steps < 3 {
		t.Fatalf("%d steps; the batches should have been cut", steps)
	}
	var got []string
	for _, r := range w.records() {
		got = append(got, r.ID)
	}
	if strings.Join(got, ",") != "b1,b2,b3,b4,b5" {
		t.Fatalf("records %v", got)
	}
	st := w.stats()
	if st.Translated != 5 || st.Skipped["same_locale"] != 1 {
		t.Fatalf("stats %+v (skips counted twice or lost)", st)
	}
}

func TestFetchEntryTooLargeIsSkippedAndCounted(t *testing.T) {
	w := newRunWorld(t)
	huge := "<p>" + strings.Repeat("word ", 30000) + "</p>"
	w.pages[""] = []entity{
		ent("huge", "en", "", fmt.Sprintf(`{"content":%q}`, huge)),
		ent("small", "en", "", `{"content":"<p>ok</p>"}`),
	}
	w.run()
	recs := w.records()
	if len(recs) != 1 || recs[0].ID != "small" {
		t.Fatalf("records %+v", recs)
	}
	if st := w.stats(); st.Skipped["too_large"] != 1 || st.Translated != 1 {
		t.Fatalf("stats %+v", st)
	}
	if len(w.failReasons) > 0 {
		t.Fatal(w.failReasons)
	}
}

func TestFetchCheckpointAndProgress(t *testing.T) {
	w := twoPageWorld(t)
	w.step()
	var cur cursor
	if err := json.Unmarshal([]byte(w.checkpoint), &cur); err != nil {
		t.Fatal(err)
	}
	if cur.C != "p2" || cur.I != 0 || cur.Seq != 4 || cur.N != 1 || len(w.checkpoint) > 4096 {
		t.Fatalf("cursor %q", w.checkpoint)
	}
	if len(w.progress) != 1 || !strings.HasPrefix(w.progress[0], "batch 1: 4 entities, ") || !strings.HasSuffix(w.progress[0], " chars") {
		t.Fatalf("progress %q", w.progress)
	}
	if w.finished {
		t.Fatal("finished after the first page")
	}
	if w.buffer["stat/000000"] == nil {
		t.Fatal("no stat record for step 0")
	}
}

func TestFetchBadLocaleFailsBeforeAnyProviderCall(t *testing.T) {
	for _, loc := range []string{"", "xx-yy-zz-aa", "tlh"} {
		w := twoPageWorld(t)
		w.params = fmt.Sprintf(`{"locale":%q,"schema":"page"}`, loc)
		w.step()
		if len(w.failReasons) != 1 || w.fetches != 0 || len(w.buffer) != 0 {
			t.Fatalf("locale %q: fail=%v fetches=%d", loc, w.failReasons, w.fetches)
		}
	}
	w := twoPageWorld(t)
	w.params = `{"locale":"de","schema":"event"}`
	w.step()
	if len(w.failReasons) != 1 || w.fetches != 0 {
		t.Fatalf("fail=%v", w.failReasons)
	}
}

func TestFetchHostNotConfirmedFailsTheRun(t *testing.T) {
	w := twoPageWorld(t)
	w.hosts = []string{"api-free.deepl.com"}
	w.step()
	if len(w.failReasons) != 1 || !strings.Contains(w.failReasons[0], "api.deepl.com was not confirmed") || w.fetches != 0 {
		t.Fatalf("fail=%v fetches=%d", w.failReasons, w.fetches)
	}
}

func TestFetchMissingKeyFailsTheRun(t *testing.T) {
	w := twoPageWorld(t)
	w.settings = `{"provider":"deepl"}`
	w.step()
	if len(w.failReasons) != 1 || !strings.Contains(w.failReasons[0], "no API key") || w.fetches != 0 {
		t.Fatalf("fail=%v", w.failReasons)
	}
}

func TestFetchProviderErrorFailsTheRunAndNothingElseRuns(t *testing.T) {
	w := twoPageWorld(t)
	w.status, w.errBody = 456, `{"message":"Quota exceeded for `+fetchTestKey+`"}`
	steps := w.run()
	if steps != 1 || len(w.failReasons) != 1 || w.failReasons[0] != "DeepL quota exceeded" {
		t.Fatalf("steps=%d fail=%v", steps, w.failReasons)
	}
	if w.finished || w.checkpoint != "" || len(w.progress) != 0 || len(w.records()) != 0 {
		t.Fatal("the failed step left progress behind")
	}
}

func TestFetchKeyNeverInFailReasonProgressOrBuffer(t *testing.T) {
	w := twoPageWorld(t)
	w.run()
	all := strings.Join(w.progress, "\n") + w.checkpoint
	for _, v := range w.buffer {
		all += string(v)
	}
	if strings.Contains(all, fetchTestKey) {
		t.Fatal("the key reached the run state")
	}
	for _, r := range w.fetchReqs {
		if strings.Contains(r.URL, fetchTestKey) {
			t.Fatal("the key is in the URL")
		}
	}
	w2 := twoPageWorld(t)
	w2.status, w2.errBody = 400, `{"message":"bad `+fetchTestKey+`"}`
	w2.run()
	if len(w2.failReasons) != 1 || strings.Contains(w2.failReasons[0], fetchTestKey) {
		t.Fatalf("fail=%v", w2.failReasons)
	}
}

func TestFetchGlossaryLossIsCounted(t *testing.T) {
	w := newRunWorld(t)
	w.settings = fmt.Sprintf(`{"api_key":%q,"glossary":[{"source":"Hatamoto","target":""}]}`, fetchTestKey)
	w.pages[""] = []entity{ent("g1", "en", "", `{"content":"<p>Hatamoto is a CMS</p>"}`)}
	w.run()
	recs := w.records()
	if len(recs) != 1 || recs[0].T.Content != "<p>Hatamoto IS A CMS</p>" {
		t.Fatalf("records %+v", recs)
	}
	if w.stats().GlossaryLost != 0 {
		t.Fatalf("stats %+v", w.stats())
	}
}
