package main

import (
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

type slugWorld struct {
	t     *testing.T
	taken map[string]bool // slug -> taken on the host
	calls []sdk.SlugTakenArgs
}

func newSlugWorld(t *testing.T, taken ...string) *slugWorld {
	w := &slugWorld{t: t, taken: map[string]bool{}}
	for _, s := range taken {
		w.taken[s] = true
	}
	callHost = w.call
	t.Cleanup(func() { callHost = nil })
	return w
}

func (w *slugWorld) call(fn string, args, out any) error {
	if fn != sdk.FnEntitiesSlugTaken {
		w.t.Fatalf("unexpected host call %q", fn)
	}
	a := args.(sdk.SlugTakenArgs)
	w.calls = append(w.calls, a)
	out.(*sdk.SlugTaken).Taken = w.taken[a.Slug]
	return nil
}

func TestSlugify(t *testing.T) {
	for in, want := range map[string]string{
		"Hello, World!":    "hello-world",
		"  --Über uns--  ": "uber-uns",
		"日本 語":             "",
		"a___b   c":        "a-b-c",
		"":                 "",
		"!!!":              "",
		"Straße 2":         "strasse-2",
		"ÄÖÜ":              "aou",
		"Giới thiệu":       "",
		"İletişim":         "iletisim",
		"Cafe\u0301":       "cafe",
	} {
		if got := slugify(in); got != want {
			t.Errorf("slugify(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSlugBaseFallsBackOnUnmappedLetters(t *testing.T) {
	if got := slugBase("Giới thiệu", "about-us"); got != "about-us" {
		t.Fatalf("got %q", got)
	}
	if got := slugBase("İletişim", "contact"); got != "iletisim" {
		t.Fatalf("got %q", got)
	}
}

func TestSlugifyLengthCap(t *testing.T) {
	got := slugify(strings.Repeat("a", 300))
	if len(got) != 200 {
		t.Fatalf("len %d", len(got))
	}
	if got := slugify(strings.Repeat("a", 199) + " b"); got != strings.Repeat("a", 199) {
		t.Fatalf("trailing hyphen kept: %q", got[190:])
	}
}

func TestPickFreeSlugUnchanged(t *testing.T) {
	w := newSlugWorld(t)
	s := newSlugSet()
	got, err := s.pick("page", "de", "ueber-uns")
	if err != nil || got != "ueber-uns" {
		t.Fatalf("%q %v", got, err)
	}
	if len(w.calls) != 1 || w.calls[0] != (sdk.SlugTakenArgs{Schema: "page", Slug: "ueber-uns", Locale: "de"}) {
		t.Fatalf("slug_taken args %+v", w.calls)
	}
}

func TestPickSuffixesOnHostCollision(t *testing.T) {
	newSlugWorld(t, "kontakt")
	got, _ := newSlugSet().pick("page", "de", "kontakt")
	if got != "kontakt-2" {
		t.Fatalf("%q", got)
	}
	newSlugWorld(t, "kontakt", "kontakt-2")
	got, _ = newSlugSet().pick("page", "de", "kontakt")
	if got != "kontakt-3" {
		t.Fatalf("%q", got)
	}
}

func TestPickTwoStagedCopiesGetDistinctSlugs(t *testing.T) {
	w := newSlugWorld(t)
	s := newSlugSet()
	a, _ := s.pick("page", "de", "start")
	n := len(w.calls)
	b, _ := s.pick("page", "de", "start")
	if a != "start" || b != "start-2" {
		t.Fatalf("%q %q", a, b)
	}
	// the in-run set answered for "start"; only "start-2" went to the host
	if len(w.calls)-n != 1 || w.calls[n].Slug != "start-2" {
		t.Fatalf("calls %+v", w.calls)
	}
	// another schema or locale is independent
	c, _ := s.pick("post", "de", "start")
	d, _ := s.pick("page", "fr", "start")
	if c != "start" || d != "start" {
		t.Fatalf("%q %q", c, d)
	}
}

func TestPickGivesUpAfterTwentySuffixes(t *testing.T) {
	taken := []string{"x"}
	for n := 2; n <= 30; n++ {
		taken = append(taken, candidate("x", n))
	}
	w := newSlugWorld(t, taken...)
	_, err := newSlugSet().pick("page", "de", "x")
	if err == nil || err.Error() != "no free slug for x" {
		t.Fatalf("%v", err)
	}
	if len(w.calls) != 20 {
		t.Fatalf("%d host checks", len(w.calls))
	}
}

func TestPickSuffixKeepsLengthCap(t *testing.T) {
	newSlugWorld(t)
	s := newSlugSet()
	base := strings.Repeat("a", 200)
	s.pick("page", "de", base)
	got, _ := s.pick("page", "de", base)
	if len(got) != 200 || !strings.HasSuffix(got, "-2") {
		t.Fatalf("%d %q", len(got), got[190:])
	}
}

func TestSlugBaseFallsBackToSource(t *testing.T) {
	if got := slugBase("", "about-us"); got != "about-us" {
		t.Fatal(got)
	}
	if got := slugBase("???", "About Us"); got != "about-us" {
		t.Fatal(got)
	}
	if got := slugBase("о нас", "about-us"); got != "about-us" {
		t.Fatal(got) // Cyrillic has no slug letters: core would publish ""
	}
	if got := slugBase("日本 語", "about-us"); got != "about-us" {
		t.Fatal(got)
	}
	if got := slugBase("Über uns", "about-us"); got != "uber-uns" {
		t.Fatal(got)
	}
}
