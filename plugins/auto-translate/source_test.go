package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

// sourceWorld is a fake host with paged entities and translation groups.
type sourceWorld struct {
	t      *testing.T
	pages  map[string][]entity // list cursor -> items
	next   map[string]string   // list cursor -> next cursor
	groups map[string][]string // entity id -> locales of its translations
	calls  []hostCall
}

func newSourceWorld(t *testing.T) *sourceWorld {
	w := &sourceWorld{t: t, pages: map[string][]entity{}, next: map[string]string{}, groups: map[string][]string{}}
	callHost = w.call
	t.Cleanup(func() { callHost = nil })
	return w
}

func (w *sourceWorld) count(fn string) (n int) {
	for _, c := range w.calls {
		if c.fn == fn {
			n++
		}
	}
	return n
}

func (w *sourceWorld) call(fn string, args, out any) error {
	w.calls = append(w.calls, hostCall{fn, args})
	var res any
	switch fn {
	case "entities.list":
		a := args.(map[string]any)
		if _, ok := a["status"]; ok {
			w.t.Fatal("entities.list must not pass a status")
		}
		if a["limit"] != listLimit {
			w.t.Fatalf("limit %v", a["limit"])
		}
		cur, _ := a["cursor"].(string)
		items, ok := w.pages[cur]
		if !ok {
			w.t.Fatalf("unknown list cursor %q", cur)
		}
		res = map[string]any{"items": items, "next_cursor": w.next[cur]}
	case "entities.translations":
		id := args.(map[string]string)["id"]
		var items []map[string]string
		for _, l := range w.groups[id] {
			items = append(items, map[string]string{"id": "x", "locale": l})
		}
		res = items
	default:
		w.t.Fatalf("unexpected host call %q", fn)
	}
	b, _ := json.Marshal(res)
	return json.Unmarshal(b, out)
}

func ent(id, locale, group string, attrs string) entity {
	e := entity{ID: id, SchemaSlug: "page", Slug: "s-" + id, Locale: locale, Name: "Name " + id, Group: group}
	if attrs != "" {
		if err := json.Unmarshal([]byte(attrs), &e.Attributes); err != nil {
			panic(err)
		}
	}
	return e
}

func ids(es []entity) (r []string) {
	for _, e := range es {
		r = append(r, e.ID)
	}
	return r
}

func TestParseParams(t *testing.T) {
	p, err := parseParams(json.RawMessage(`{"locale":" de ","schema":"page"}`))
	if err != nil || p != (params{Locale: "de", Schema: "page"}) {
		t.Fatalf("%+v %v", p, err)
	}
	for _, v := range []string{`true`, `"true"`} {
		p, _ = parseParams(json.RawMessage(`{"locale":"de","schema":"post","only_missing":` + v + `}`))
		if !p.OnlyMissing {
			t.Fatalf("only_missing %s not read", v)
		}
	}
	if _, err := parseParams(json.RawMessage(`[`)); err == nil {
		t.Fatal("want an error")
	}
}

func TestIsBuilderOnlyAndTranslatable(t *testing.T) {
	cases := []struct {
		attrs   string
		builder bool
	}{
		{``, false},
		{`{"content":"<p>x</p>"}`, false},
		{`{"page_doc":{"a":1}}`, true},
		{`{"page_sections":[{"t":"hero"}],"content":"  "}`, true},
		{`{"page_doc":{"a":1},"content":"<p>x</p>"}`, false},
		{`{"page_doc":null,"page_sections":[]}`, false},
	}
	for _, c := range cases {
		if got := isBuilderOnly(ent("1", "en", "", c.attrs)); got != c.builder {
			t.Fatalf("%s: builder=%v", c.attrs, got)
		}
	}
	if ok, r := translatable(ent("1", "DE", "", ""), "de"); ok || r != "same_locale" {
		t.Fatalf("%v %s", ok, r)
	}
	if ok, r := translatable(ent("1", "en", "", `{"page_doc":{"a":1}}`), "de"); ok || r != "builder" {
		t.Fatalf("%v %s", ok, r)
	}
	e := ent("1", "en", "", "")
	e.Name = " "
	if ok, _ := translatable(e, "de"); ok {
		t.Fatal("empty name must be skipped")
	}
	if ok, _ := translatable(ent("1", "en", "", ""), "de"); !ok {
		t.Fatal("plain entity must be a candidate")
	}
}

func TestNextCandidatesPagesByCursorNotCount(t *testing.T) {
	w := newSourceWorld(t)
	w.pages[""] = []entity{ent("a", "en", "", ""), ent("b", "en", "", "")} // fewer than the limit, more pages follow
	w.next[""] = "c1"
	w.pages["c1"] = nil // everything on it was filtered out by the host
	w.next["c1"] = "c2"
	w.pages["c2"] = []entity{ent("c", "en", "", "")}
	p := params{Locale: "de", Schema: "page"}

	cands, cur, done, _, err := nextCandidates(p, cursor{Seq: 7}, 20)
	if err != nil || done || cur != (cursor{C: "c1", I: 0, Seq: 7}) || len(cands) != 2 {
		t.Fatalf("step 1: %v %+v %v %v", ids(cands), cur, done, err)
	}
	cands, cur, done, _, _ = nextCandidates(p, cur, 20)
	if done || cur.C != "c2" || len(cands) != 0 {
		t.Fatalf("step 2: %v %+v %v", ids(cands), cur, done)
	}
	cands, cur, done, _, _ = nextCandidates(p, cur, 20)
	if !done || len(cands) != 1 || cands[0].ID != "c" {
		t.Fatalf("step 3: %v %+v %v", ids(cands), cur, done)
	}
}

func TestNextCandidatesMaxResumesInsidePage(t *testing.T) {
	w := newSourceWorld(t)
	w.pages[""] = []entity{ent("a", "en", "", ""), ent("b", "en", "", ""), ent("c", "en", "", "")}
	p := params{Locale: "de", Schema: "page"}
	cands, cur, done, _, _ := nextCandidates(p, cursor{}, 2)
	if done || len(cands) != 2 || cur.I != 2 || cur.C != "" {
		t.Fatalf("%v %+v %v", ids(cands), cur, done)
	}
	cands, cur, done, _, _ = nextCandidates(p, cur, 2)
	if !done || len(cands) != 1 || cands[0].ID != "c" {
		t.Fatalf("%v %+v %v", ids(cands), cur, done)
	}
}

func TestNextCandidatesOnlyMissing(t *testing.T) {
	w := newSourceWorld(t)
	w.pages[""] = []entity{
		ent("done", "en", "g1", ""),
		ent("todo", "en", "g2", ""),
		ent("alone", "en", "", ""),
	}
	w.groups["done"] = []string{"en", "DE"}
	w.groups["todo"] = []string{"en", "fr"}
	p := params{Locale: "de", Schema: "page", OnlyMissing: true}

	cands, _, done, counts, err := nextCandidates(p, cursor{}, 20)
	if err != nil || !done {
		t.Fatal(err, done)
	}
	if got := ids(cands); len(got) != 2 || got[0] != "todo" || got[1] != "alone" {
		t.Fatalf("candidates %v", got)
	}
	if counts["has_translation"] != 1 {
		t.Fatalf("counts %v", counts)
	}
	if n := w.count("entities.translations"); n != 2 {
		t.Fatalf("%d translations calls (none for the entity without a group)", n)
	}
}

func TestNextCandidatesWithoutOnlyMissingNeverReadsTranslations(t *testing.T) {
	w := newSourceWorld(t)
	w.pages[""] = []entity{ent("a", "en", "g1", "")}
	w.groups["a"] = []string{"en", "de"}
	cands, _, _, _, err := nextCandidates(params{Locale: "de", Schema: "page"}, cursor{}, 20)
	if err != nil || len(cands) != 1 || w.count("entities.translations") != 0 {
		t.Fatal(err, ids(cands), w.count("entities.translations"))
	}
}

func TestNextCandidatesCountsSkips(t *testing.T) {
	w := newSourceWorld(t)
	w.pages[""] = []entity{
		ent("b1", "en", "", `{"page_doc":{"x":1}}`),
		ent("same", "de", "", ""),
		ent("ok", "en", "", `{"content":"<p>x</p>"}`),
		ent("b2", "en", "", `{"page_sections":[{"k":1}]}`),
	}
	cands, _, _, counts, _ := nextCandidates(params{Locale: "de", Schema: "page"}, cursor{}, 20)
	if len(cands) != 1 || cands[0].ID != "ok" || counts["builder"] != 2 || counts["same_locale"] != 1 {
		t.Fatalf("%v %v", ids(cands), counts)
	}
}

func TestNextCandidatesReadCapStopsWithResumableCursor(t *testing.T) {
	w := newSourceWorld(t)
	var items []entity
	for i := 0; i < 250; i++ {
		items = append(items, ent(fmt.Sprintf("e%03d", i), "en", "g", ""))
	}
	w.pages[""] = items
	p := params{Locale: "de", Schema: "page", OnlyMissing: true}

	seen := map[string]bool{}
	cur := cursor{}
	for step := 0; step < 5; step++ {
		w.calls = nil
		cands, next, done, _, err := nextCandidates(p, cur, 1000)
		if err != nil {
			t.Fatal(err)
		}
		if n := len(w.calls); n > 190 {
			t.Fatalf("step %d: %d reads", step, n)
		}
		for _, c := range cands {
			if seen[c.ID] {
				t.Fatalf("%s twice", c.ID)
			}
			seen[c.ID] = true
		}
		if done {
			break
		}
		if next.I <= cur.I && next.C == cur.C {
			t.Fatalf("no progress: %+v -> %+v", cur, next)
		}
		cur = next
	}
	if len(seen) != 250 {
		t.Fatalf("%d of 250 seen", len(seen))
	}
}

func TestCursorSize(t *testing.T) {
	c := cursor{I: 19, Seq: 99999999}
	for i := 0; i < 3000; i++ {
		c.C += "x"
	}
	b, _ := json.Marshal(c)
	if len(b) > 4096 {
		t.Fatalf("%d bytes", len(b))
	}
	var back cursor
	if err := json.Unmarshal(b, &back); err != nil || back != c {
		t.Fatalf("round trip %+v %v", back, err)
	}
}
