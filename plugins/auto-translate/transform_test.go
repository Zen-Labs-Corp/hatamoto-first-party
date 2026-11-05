package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// stageWorld is a fake host for the transform phase: a run buffer to read, a
// site, the host's slug check and a recorder of staged copies. Functions that
// are denied in the transform phase fail the test.
type stageWorld struct {
	t          *testing.T
	params     string
	site       sdk.SiteInfo
	buffer     map[string][]byte
	taken      map[string]bool   // slugs held by published entities
	upsertErr  map[string]string // source id -> host error code
	upserts    []sdk.EntityUpsert
	payloads   []string
	checkpoint string
	progress   []string
	summary    map[string]any
	finished   bool
	fails      []string
	seq        int
}

func newStageWorld(t *testing.T) *stageWorld {
	w := &stageWorld{
		t:         t,
		params:    `{"locale":"de","schema":"page"}`,
		site:      sdk.SiteInfo{Title: "Site", Language: "en", Locales: []string{"en", "de"}},
		buffer:    map[string][]byte{},
		taken:     map[string]bool{},
		upsertErr: map[string]string{},
	}
	callHost = w.call
	t.Cleanup(func() { callHost = nil })
	return w
}

// add stores a record the way the fetch phase would and returns its key.
func (w *stageWorld) add(id, parent, name, slug string, mod ...func(*record)) string {
	r := record{
		ID: id, SchemaSlug: "page", SourceLocale: "en", SourceSlug: "src-" + id, TargetLocale: "de",
		ParentID:   parent,
		Attributes: map[string]json.RawMessage{"layout": json.RawMessage(`"wide"`)},
		T:          translated{Name: name, Slug: slug, Content: "<p>" + name + "</p>"},
	}
	for _, m := range mod {
		m(&r)
	}
	b, _ := json.Marshal(r)
	key := bufferKey(w.seq, id, parent)
	w.seq++
	w.buffer[key] = b
	return key
}

func (w *stageWorld) addStat(n int, s fetchStat) {
	b, _ := json.Marshal(s)
	w.buffer[fmt.Sprintf("stat/%06d", n)] = b
}

func (w *stageWorld) call(fn string, args, out any) error {
	var res any
	switch fn {
	case sdk.FnRunInfo:
		res = sdk.RunInfo{RunID: "r1", Phase: "transform", Params: json.RawMessage(w.params)}
	case sdk.FnSiteInfo:
		res = w.site
	case sdk.FnBufferList:
		a := args.(sdk.BufferListArgs)
		var keys []string
		for k := range w.buffer {
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
	case sdk.FnBufferGet:
		v, ok := w.buffer[args.(sdk.BufferGetArgs).Key]
		res = sdk.BufferValue{Found: ok, ValueB64: base64.StdEncoding.EncodeToString(v)}
	case sdk.FnEntitiesSlugTaken:
		res = sdk.SlugTaken{Taken: w.taken[args.(sdk.SlugTakenArgs).Slug]}
	case sdk.FnChangesetEntityUpsert:
		up := args.(sdk.EntityUpsert)
		b, _ := json.Marshal(up)
		w.payloads = append(w.payloads, string(b))
		if code := w.upsertErr[up.TranslationOf]; code != "" {
			return &sdk.HostError{Code: code}
		}
		w.upserts = append(w.upserts, up)
		res = sdk.UpsertResult{SourceKey: up.SourceKey, Created: true}
	case sdk.FnRunCheckpoint:
		w.checkpoint = args.(sdk.RunCheckpointArgs).Cursor
	case sdk.FnRunProgress:
		w.progress = append(w.progress, args.(sdk.RunProgressArgs).Message)
	case sdk.FnRunFinish:
		w.finished = true
		if raw := args.(sdk.RunFinishArgs).Summary; raw != nil {
			if err := json.Unmarshal(raw, &w.summary); err != nil {
				w.t.Fatal(err)
			}
		}
	case sdk.FnRunFail:
		w.fails = append(w.fails, args.(sdk.RunFailArgs).Reason)
	case "http.fetch", sdk.FnBufferPut, sdk.FnEntitiesList:
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

func (w *stageWorld) step() {
	w.t.Helper()
	in, _ := json.Marshal(sdk.RunStep{RunID: "r1", Phase: sdk.RunPhaseTransform, Cursor: w.checkpoint})
	if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
		w.t.Fatalf("step returned an error: %v", err)
	}
}

func (w *stageWorld) run() int {
	w.t.Helper()
	for n := 1; n <= 30; n++ {
		w.step()
		if w.finished || len(w.fails) > 0 {
			return n
		}
	}
	w.t.Fatal("the phase did not finish")
	return 0
}

func (w *stageWorld) slugs() map[string]string {
	m := map[string]string{}
	for _, u := range w.upserts {
		m[u.TranslationOf] = u.Slug
	}
	return m
}

func TestTransformStagesLinkedCopies(t *testing.T) {
	w := newStageWorld(t)
	w.site.Locales = []string{"en", "de-AT"}
	w.params = `{"locale":"de_at","schema":"page"}`
	w.add("p1", "", "Über uns", "ueber-uns", func(r *record) { r.T.MetaTitle = "MT" })
	w.add("p2", "", "Kontakt", "kontakt")
	if n := w.run(); n != 1 {
		t.Fatalf("%d steps", n)
	}
	if len(w.upserts) != 2 || !w.finished {
		t.Fatalf("upserts=%d finished=%v fails=%v", len(w.upserts), w.finished, w.fails)
	}
	u := w.upserts[0]
	if u.SourceKey != "tr:de-at:p1" || u.TranslationOf != "p1" || u.Locale != "de-AT" || u.Schema != "page" ||
		u.Slug != "ueber-uns" || u.Name != "Über uns" || u.ParentKey != "" {
		t.Fatalf("upsert %+v", u)
	}
	var attrs map[string]string
	if err := json.Unmarshal(u.Attributes, &attrs); err != nil {
		t.Fatal(err)
	}
	if attrs["layout"] != "wide" || attrs["content"] != "<p>Über uns</p>" || attrs["meta_title"] != "MT" || len(attrs) != 3 {
		t.Fatalf("attributes %v", attrs)
	}
	if u.SEO == nil || u.SEO.MetaTitle != "MT" || w.upserts[1].SEO != nil {
		t.Fatalf("seo %+v / %+v", u.SEO, w.upserts[1].SEO)
	}
}

func TestTransformSendsNoStatusAndOnlyKnownFields(t *testing.T) {
	w := newStageWorld(t)
	w.add("p1", "", "Eins", "eins")
	w.run()
	for _, p := range w.payloads {
		var m map[string]json.RawMessage
		if err := json.Unmarshal([]byte(p), &m); err != nil {
			t.Fatal(err)
		}
		if _, ok := m["status"]; ok {
			t.Fatalf("status in %s", p)
		}
		for k := range m {
			switch k {
			case "source_key", "schema", "slug", "locale", "translation_of", "name", "attributes", "seo", "parent_key":
			default:
				t.Fatalf("unexpected field %q", k)
			}
		}
	}
}

func TestTransformSlugCollisions(t *testing.T) {
	w := newStageWorld(t)
	w.taken["kontakt"] = true
	w.add("a", "", "Kontakt", "kontakt")
	w.add("b", "", "Start", "start")
	w.add("c", "", "Start", "start")
	w.add("d", "", "", "", func(r *record) { r.SourceSlug = "from-source" })
	w.run()
	got := w.slugs()
	want := map[string]string{"a": "kontakt-2", "b": "start", "c": "start-2", "d": "from-source"}
	for id, s := range want {
		if got[id] != s {
			t.Fatalf("slugs %v, want %v", got, want)
		}
	}
}

func TestTransformParentsFirstWithParentKey(t *testing.T) {
	w := newStageWorld(t)
	w.add("child", "root", "Kind", "kind")
	w.add("grand", "child", "Enkel", "enkel")
	w.add("orphan", "elsewhere", "Waise", "waise")
	w.add("root", "", "Wurzel", "wurzel")
	w.run()
	var order []string
	keys := map[string]string{}
	for _, u := range w.upserts {
		order = append(order, u.TranslationOf)
		keys[u.TranslationOf] = u.ParentKey
	}
	pos := func(id string) int {
		for i, o := range order {
			if o == id {
				return i
			}
		}
		return -1
	}
	if !(pos("root") < pos("child") && pos("child") < pos("grand")) {
		t.Fatalf("order %v", order)
	}
	if keys["child"] != "tr:de:root" || keys["grand"] != "tr:de:child" || keys["root"] != "" || keys["orphan"] != "" {
		t.Fatalf("parent keys %v", keys)
	}
}

func TestTransformParentCycleDoesNotHang(t *testing.T) {
	w := newStageWorld(t)
	w.add("a", "b", "A", "a")
	w.add("b", "a", "B", "b")
	w.run()
	if len(w.upserts) != 2 || len(w.fails) > 0 {
		t.Fatalf("upserts=%d fails=%v", len(w.upserts), w.fails)
	}
}

func TestTransformLocaleCheckStagesNothing(t *testing.T) {
	for name, tc := range map[string]struct{ locales []string }{
		"not served":    {[]string{"en", "fr"}},
		"site language": {[]string{"en", "de"}},
	} {
		w := newStageWorld(t)
		w.site.Locales = tc.locales
		if name == "site language" {
			w.site.Language = "de"
		}
		w.add("p1", "", "Eins", "eins", func(r *record) { r.SourceLocale = w.site.Language })
		w.run()
		if len(w.fails) != 1 || !strings.Contains(w.fails[0], "is not one of this site's locales") || len(w.payloads) != 0 || w.finished {
			t.Fatalf("%s: fails=%v upserts=%d", name, w.fails, len(w.payloads))
		}
	}
}

func TestTransformDropsOtherLanguageRecords(t *testing.T) {
	w := newStageWorld(t)
	w.add("en1", "", "Eins", "eins")
	w.add("fr1", "", "Un", "un", func(r *record) { r.SourceLocale = "fr" })
	w.run()
	if len(w.upserts) != 1 || w.upserts[0].TranslationOf != "en1" {
		t.Fatalf("upserts %+v", w.upserts)
	}
	if got := w.summary["skipped"].(map[string]any)["other_language"]; got != float64(1) {
		t.Fatalf("summary %v", w.summary)
	}
}

func TestTransformManyRecordsKeepSlugsUniqueAcrossSteps(t *testing.T) {
	w := newStageWorld(t)
	w.taken["dup"] = true
	for i := 0; i < 60; i++ {
		name, slug := fmt.Sprintf("T%d", i), fmt.Sprintf("t%d", i)
		switch i {
		case 3, 27, 52: // the same title, one before and two after the first step boundary
			name, slug = "Same", "same"
		case 24, 26, 55: // a published entity holds the slug; two of these come after a boundary
			name, slug = "Dup", "dup"
		}
		w.add(fmt.Sprintf("id%02d", i), "", name, slug)
	}
	steps := w.run()
	if steps != 3 || len(w.upserts) != 60 || len(w.fails) > 0 {
		t.Fatalf("steps=%d upserts=%d fails=%v", steps, len(w.upserts), w.fails)
	}
	seen := map[string]string{}
	for id, s := range w.slugs() {
		if other, dup := seen[s]; dup {
			t.Fatalf("slug %q used by %s and %s", s, other, id)
		}
		seen[s] = id
	}
	got := w.slugs()
	if got["id03"] != "same" || got["id27"] != "same-2" || got["id52"] != "same-3" {
		t.Fatalf("same: %v %v %v", got["id03"], got["id27"], got["id52"])
	}
	if got["id24"] != "dup-2" || got["id26"] != "dup-3" || got["id55"] != "dup-4" {
		t.Fatalf("dup: %v %v %v", got["id24"], got["id26"], got["id55"])
	}
	// no step staged more than 25 and the cursor stayed small
	if len(w.checkpoint) > 4096 {
		t.Fatalf("cursor %d bytes", len(w.checkpoint))
	}
}

func TestTransformCursorTooLargeFailsTheRun(t *testing.T) {
	w := newStageWorld(t)
	// A cursor with more collision suffixes than fit in 4 KiB.
	for i := 0; i < 30; i++ {
		w.add(fmt.Sprintf("i%02d", i), "", "N", fmt.Sprintf("n%d", i))
	}
	big := map[string]int{}
	for i := 0; i < 600; i++ {
		big[fmt.Sprint(i)] = 2
	}
	b, _ := json.Marshal(tcursor{I: 0, Suffix: big})
	w.checkpoint = string(b)
	w.step()
	if len(w.fails) != 1 || w.fails[0] != "too many slug collisions to resume" {
		t.Fatalf("fails=%v", w.fails)
	}
}

func TestTransformSummaryAddsFetchStats(t *testing.T) {
	w := newStageWorld(t)
	w.add("p1", "", "Eins", "eins")
	w.add("p2", "", "Zwei", "zwei")
	w.addStat(0, fetchStat{Translated: 1, Skipped: map[string]int{"builder": 2, "same_locale": 1}, GlossaryLost: 1})
	w.addStat(1, fetchStat{Translated: 1, Skipped: map[string]int{"builder": 1, "has_translation": 4, "too_large": 1}, GlossaryLost: 2})
	w.upsertErr["p2"] = sdk.ErrNotOwned
	w.run()
	sk := w.summary["skipped"].(map[string]any)
	want := map[string]float64{"builder": 3, "same_locale": 1, "has_translation": 4, "too_large": 1, "other_language": 0, "conflict": 1}
	for k, v := range want {
		if sk[k] != v {
			t.Fatalf("skipped %v, want %v", sk, want)
		}
	}
	if w.summary["staged"] != float64(1) || w.summary["glossary_terms_lost"] != float64(3) ||
		w.summary["locale"] != "de" || w.summary["schema"] != "page" {
		t.Fatalf("summary %v", w.summary)
	}
	if len(w.fails) > 0 {
		t.Fatalf("a refused copy failed the run: %v", w.fails)
	}
}

func TestTransformOtherUpsertErrorFailsTheRun(t *testing.T) {
	w := newStageWorld(t)
	w.add("p1", "", "Eins", "eins")
	w.upsertErr["p1"] = sdk.ErrInternal
	w.run()
	if len(w.fails) != 1 || w.finished {
		t.Fatalf("fails=%v finished=%v", w.fails, w.finished)
	}
}

func TestTransformProgressText(t *testing.T) {
	w := newStageWorld(t)
	for i := 0; i < 30; i++ {
		w.add(fmt.Sprintf("i%02d", i), "", fmt.Sprintf("N%d", i), fmt.Sprintf("n%d", i))
	}
	w.step()
	if len(w.progress) != 1 || w.progress[0] != "staged 25 of 30" || w.finished {
		t.Fatalf("progress %v finished=%v", w.progress, w.finished)
	}
}

func TestTransformRerunGivesSameSourceKeys(t *testing.T) {
	keys := func() []string {
		w := newStageWorld(t)
		w.add("p1", "", "Eins", "eins")
		w.add("p2", "p1", "Zwei", "zwei")
		w.run()
		var ks []string
		for _, u := range w.upserts {
			ks = append(ks, u.SourceKey+"|"+u.ParentKey)
		}
		return ks
	}
	a, b := keys(), keys()
	if strings.Join(a, ",") != strings.Join(b, ",") || len(a) != 2 {
		t.Fatalf("%v vs %v", a, b)
	}
}

func TestTransformEmptyBufferFinishes(t *testing.T) {
	w := newStageWorld(t)
	w.run()
	if !w.finished || w.summary["staged"] != float64(0) {
		t.Fatalf("finished=%v summary=%v", w.finished, w.summary)
	}
}
