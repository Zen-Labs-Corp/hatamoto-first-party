package main

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"testing"
	"unicode"
)

// fakeTranslate upper-cases the text nodes of every text, as a provider
// would translate them, and leaves tags, entities and translate="no" spans
// alone.
func fakeTranslate(texts []string) []string {
	out := make([]string, len(texts))
	for i, s := range texts {
		var b strings.Builder
		skip := false
		for _, seg := range scanHTML(s) {
			switch {
			case seg.markup:
				skip = strings.Contains(seg.text, `translate="no"`)
				b.WriteString(seg.text)
			case skip:
				skip = false
				b.WriteString(seg.text)
			default:
				b.WriteString(upperText(seg.text))
			}
		}
		out[i] = b.String()
	}
	return out
}

func upperText(s string) string {
	rs := []rune(s)
	var b strings.Builder
	for i := 0; i < len(rs); {
		if rs[i] == '&' {
			if j := entityEnd(rs, i); j > i {
				b.WriteString(string(rs[i:j]))
				i = j
				continue
			}
		}
		b.WriteRune(unicode.ToUpper(rs[i]))
		i++
	}
	return b.String()
}

func markupOf(s string) string {
	var b strings.Builder
	for _, seg := range scanHTML(s) {
		if seg.markup {
			b.WriteString(seg.text)
		}
	}
	return b.String()
}

func rawJSON(v string) json.RawMessage { return json.RawMessage(v) }

func TestUnitForAndFillGoldenBody(t *testing.T) {
	body := goldenBody + `<p>Run hatamoto with the cms.</p>`
	e := entity{
		ID: "11111111-1111-1111-1111-111111111111", SchemaSlug: "page", Slug: "my-first-page",
		Locale: "en", Name: "My <b>first</b> & page", Group: "g1", ParentID: "22222222-2222-2222-2222-222222222222",
		Attributes: map[string]json.RawMessage{
			"content":          mustJSON(body),
			"excerpt":          mustJSON(""),
			"meta_title":       mustJSON("Tom & Jerry"),
			"meta_description": mustJSON("About the cms"),
			"hero":             rawJSON(`{"image":"a.png",  "alt":"x"}`),
			"sort":             rawJSON(` 3 `),
		},
	}
	g := []glossaryEntry{{"Hatamoto", ""}, {"cms", "CMS"}}
	texts, slots := unitFor(e, g)

	var fields []string
	for _, s := range slots {
		fields = append(fields, s.field)
	}
	if got := strings.Join(fields, ","); got != "name,slug,content,meta_title,meta_description" {
		t.Fatalf("slots %s (empty excerpt must not be sent)", got)
	}
	if len(texts) != len(slots) {
		t.Fatal("texts and slots differ")
	}
	if texts[1] != "my first page" {
		t.Fatalf("slug text %q", texts[1])
	}
	if slots[2].n != 2 || !strings.Contains(texts[2], `data-hm-g="0"`) {
		t.Fatalf("content not protected: n=%d", slots[2].n)
	}
	if strings.Contains(texts[0], "<b>") {
		t.Fatalf("plain name must be escaped: %q", texts[0])
	}

	r, lost := fill(e, fakeTranslate(texts), slots, g)
	if lost != 0 {
		t.Fatalf("lost %d", lost)
	}
	if markupOf(r.T.Content) != markupOf(body) {
		t.Fatalf("markup changed:\n%q\n%q", markupOf(r.T.Content), markupOf(body))
	}
	if !strings.Contains(r.T.Content, "RUN Hatamoto WITH THE CMS.") {
		t.Fatalf("glossary not applied: %q", r.T.Content[len(r.T.Content)-60:])
	}
	if strings.Contains(r.T.Content, "data-hm-g") {
		t.Fatal("wrapper left in content")
	}
	if r.T.Name != "MY <B>FIRST</B> & PAGE" {
		t.Fatalf("name %q", r.T.Name)
	}
	if r.T.Slug != "MY FIRST PAGE" {
		t.Fatalf("slug %q", r.T.Slug)
	}
	if r.T.MetaTitle != "TOM & JERRY" {
		t.Fatalf("plain field must be unescaped: %q", r.T.MetaTitle)
	}
	if r.T.MetaDescription != "ABOUT THE CMS" {
		t.Fatalf("meta description %q", r.T.MetaDescription)
	}
	if r.T.Excerpt != "" {
		t.Fatalf("excerpt %q", r.T.Excerpt)
	}
	if r.ID != e.ID || r.SourceLocale != "en" || r.Group != "g1" || r.ParentID != e.ParentID || r.SchemaSlug != "page" {
		t.Fatalf("record %+v", r)
	}
}

func mustJSON(v any) json.RawMessage {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func TestFillKeepsOtherAttributesByteForByte(t *testing.T) {
	e := entity{ID: "i", Name: "N", Locale: "en", Attributes: map[string]json.RawMessage{
		"content":  mustJSON("<p>a</p>"),
		"hero":     rawJSON(`{"image":"a.png",  "alt":"x"}`),
		"sort":     rawJSON(` 3 `),
		"excerpt":  rawJSON(`null`), // not a string: not translated, kept
		"page_doc": rawJSON(`{"a":[1,2]}`),
	}}
	texts, slots := unitFor(e, nil)
	r, _ := fill(e, fakeTranslate(texts), slots, nil)
	if _, ok := r.Attributes["content"]; ok {
		t.Fatal("translated key must be removed from Attributes")
	}
	for _, k := range []string{"hero", "sort", "excerpt", "page_doc"} {
		if string(r.Attributes[k]) != string(e.Attributes[k]) {
			t.Fatalf("%s changed: %q", k, r.Attributes[k])
		}
	}
	if len(r.Attributes) != 4 {
		t.Fatalf("attributes %v", r.Attributes)
	}
}

func TestFillCountsLostGlossaryTerms(t *testing.T) {
	e := entity{ID: "i", Name: "N", Locale: "en", Attributes: map[string]json.RawMessage{
		"content": mustJSON("<p>Hatamoto and hatamoto</p>"),
	}}
	g := []glossaryEntry{{"Hatamoto", ""}}
	texts, slots := unitFor(e, g)
	if slots[len(slots)-1].n != 2 {
		t.Fatal("two terms expected")
	}
	// The provider dropped both wrappers.
	out := append([]string{}, texts...)
	out[len(out)-1] = "<p>Hatamoto und hatamoto</p>"
	r, lost := fill(e, out, slots, g)
	if lost != 2 || r.T.Content != "<p>Hatamoto und hatamoto</p>" {
		t.Fatalf("lost=%d content=%q", lost, r.T.Content)
	}
}

func TestBufferKey(t *testing.T) {
	id := "11111111-1111-1111-1111-111111111111"
	parent := "22222222-2222-2222-2222-222222222222"
	r := record{ID: id, ParentID: parent, SourceLocale: "en", T: translated{Slug: "Über uns"}}
	if got := bufferKey(42, r); got != "src/00000042/"+id+"/"+parent+"/en/nuber-uns" {
		t.Fatalf("key %q", got)
	}
	r = record{ID: id, SourceLocale: "en", SourceSlug: "about", ExistingSlug: "kontakt"}
	if got := bufferKey(1, r); got != "src/00000001/"+id+"/-/en/ekontakt" {
		t.Fatalf("key %q", got)
	}
	r = record{ID: id, ParentID: parent, SourceLocale: "en-GB-oxendict", T: translated{Slug: strings.Repeat("a", 300)}}
	if n := len(bufferKey(99999999, r)); n > 256 {
		t.Fatalf("%d bytes", n)
	}
}

func TestPutRecord(t *testing.T) {
	var gotKey string
	var gotValue []byte
	callHost = func(fn string, args, out any) error {
		if fn != "buffer.put" {
			t.Fatalf("unexpected host call %q", fn)
		}
		m := mustJSON(args)
		var a struct{ Key, ValueB64 string }
		_ = json.Unmarshal([]byte(strings.NewReplacer("value_b64", "ValueB64", `"key"`, `"Key"`).Replace(string(m))), &a)
		gotKey = a.Key
		gotValue, _ = base64.StdEncoding.DecodeString(a.ValueB64)
		return nil
	}
	t.Cleanup(func() { callHost = nil })

	r := record{ID: "i1", SchemaSlug: "page", SourceLocale: "en", TargetLocale: "de", T: translated{Name: "Name"}}
	if err := putRecord(3, r); err != nil {
		t.Fatal(err)
	}
	if gotKey != "src/00000003/i1/-/en/ni1" {
		t.Fatalf("key %q", gotKey)
	}
	var back record
	if err := json.Unmarshal(gotValue, &back); err != nil || back.T.Name != "Name" || back.TargetLocale != "de" {
		t.Fatalf("stored %s (%v)", gotValue, err)
	}

	r.T.Content = strings.Repeat("x", 800<<10)
	if err := putRecord(4, r); err != errRecordTooLarge {
		t.Fatalf("err=%v", err)
	}
}

// Excerpt and description are plain text: they go out escaped and come back
// without the entities a provider adds.
func TestFillTreatsExcerptAndDescriptionAsPlainText(t *testing.T) {
	e := entity{ID: "i", Name: "N", Locale: "en", Attributes: map[string]json.RawMessage{
		"excerpt":     mustJSON("Q&A <beta>"),
		"description": mustJSON("plain"),
	}}
	texts, slots := unitFor(e, nil)
	if texts[1] != "Q&amp;A &lt;beta&gt;" {
		t.Fatalf("sent %q", texts[1])
	}
	out := append([]string(nil), texts...)
	out[1] = "l&#39;&eacute;t&eacute; &amp; <b>Q</b>"
	out[2] = "d&#39;un &quot;site&quot;"
	r, _ := fill(e, out, slots, nil)
	if r.T.Excerpt != "l'été & Q" || r.T.Description != `d'un "site"` {
		t.Fatalf("excerpt %q description %q", r.T.Excerpt, r.T.Description)
	}
}
