package main

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk/guest"
)

const (
	maxSlugBytes    = 200
	maxSlugSuffixes = 20
)

// slugify lower-cases s, keeps letters and digits of any script and turns
// every other run of characters into one hyphen. The result has no leading or
// trailing hyphen and is at most 200 bytes, cut on a rune boundary.
func slugify(s string) string {
	var b strings.Builder
	dash := true // swallows a leading hyphen
	for _, r := range strings.ToLower(s) {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
			dash = false
		} else if !dash {
			b.WriteByte('-')
			dash = true
		}
	}
	return strings.Trim(cutBytes(b.String(), maxSlugBytes), "-")
}

// cutBytes shortens s to at most n bytes without splitting a rune.
func cutBytes(s string, n int) string {
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}

// slugBase is the slug to build from: the translated words, or the source
// slug when the translation produced none.
func slugBase(translated, source string) string {
	if s := slugify(translated); s != "" {
		return s
	}
	return slugify(source)
}

// slugSet remembers the slugs chosen in this run. The host's slug check only
// sees published entities, never the copies this run has staged.
type slugSet struct{ used map[string]bool }

func newSlugSet() *slugSet { return &slugSet{used: map[string]bool{}} }

func slugKey(schema, locale, slug string) string { return schema + "|" + locale + "|" + slug }

// add records a slug as taken without asking the host.
func (s *slugSet) add(schema, locale, slug string) { s.used[slugKey(schema, locale, slug)] = true }

// candidate is the n-th try for base: base, base-2, base-3 ...
func candidate(base string, n int) string {
	if n <= 1 {
		return base
	}
	suffix := fmt.Sprintf("-%d", n)
	return strings.TrimRight(cutBytes(base, maxSlugBytes-len(suffix)), "-") + suffix
}

// pick returns the first free slug for base and records it. A slug is taken
// when this run already chose it or when the host says a published entity
// holds it. At most 20 candidates are tried.
func (s *slugSet) pick(schema, locale, base string) (string, error) {
	if base == "" {
		return "", fmt.Errorf("no slug to build from")
	}
	for n := 1; n <= maxSlugSuffixes; n++ {
		c := candidate(base, n)
		if s.used[slugKey(schema, locale, c)] {
			continue
		}
		taken, err := guest.SlugTaken(callHost, schema, c, locale)
		if err != nil {
			return "", fmt.Errorf("the slug check failed: %w", hostReason(err))
		}
		if !taken {
			s.add(schema, locale, c)
			return c, nil
		}
	}
	return "", fmt.Errorf("no free slug for %s", base)
}

// pickLocal is pick without the host check: it replays a choice against the
// set alone (used to rebuild the set when a run resumes).
func (s *slugSet) pickLocal(schema, locale, base string) string {
	for n := 1; ; n++ {
		c := candidate(base, n)
		if !s.used[slugKey(schema, locale, c)] {
			s.add(schema, locale, c)
			return c
		}
	}
}
