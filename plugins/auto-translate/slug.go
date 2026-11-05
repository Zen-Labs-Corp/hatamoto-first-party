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

// latinFold turns common Latin letters with diacritics into the ASCII letters
// core's slug alphabet keeps. Core drops everything outside [a-z0-9-] when a
// copy is published, so a letter left as is would vanish from the live URL.
var latinFold = strings.NewReplacer(
	"ß", "ss", "æ", "ae", "œ", "oe", "ø", "o", "đ", "d", "ð", "d", "þ", "th", "ł", "l", "ı", "i",
	"à", "a", "á", "a", "â", "a", "ã", "a", "ä", "a", "å", "a", "ā", "a", "ă", "a", "ą", "a",
	"ç", "c", "ć", "c", "ĉ", "c", "ċ", "c", "č", "c", "ď", "d",
	"è", "e", "é", "e", "ê", "e", "ë", "e", "ē", "e", "ĕ", "e", "ė", "e", "ę", "e", "ě", "e",
	"ĝ", "g", "ğ", "g", "ġ", "g", "ģ", "g", "ĥ", "h",
	"ì", "i", "í", "i", "î", "i", "ï", "i", "ĩ", "i", "ī", "i", "ĭ", "i", "į", "i", "ĵ", "j", "ķ", "k",
	"ĺ", "l", "ļ", "l", "ľ", "l", "ñ", "n", "ń", "n", "ņ", "n", "ň", "n",
	"ò", "o", "ó", "o", "ô", "o", "õ", "o", "ö", "o", "ō", "o", "ŏ", "o", "ő", "o",
	"ŕ", "r", "ŗ", "r", "ř", "r", "ś", "s", "ŝ", "s", "ş", "s", "š", "s", "ș", "s",
	"ţ", "t", "ť", "t", "ț", "t",
	"ù", "u", "ú", "u", "û", "u", "ü", "u", "ũ", "u", "ū", "u", "ŭ", "u", "ů", "u", "ű", "u", "ų", "u",
	"ŵ", "w", "ý", "y", "ÿ", "y", "ŷ", "y", "ź", "z", "ż", "z", "ž", "z",
)

// slugify builds a slug in core's alphabet: it lower-cases s, folds Latin
// diacritics to ASCII, keeps only a-z, 0-9 and hyphens, and turns every other
// run of characters into one hyphen. Letters of other scripts are not kept, so
// the result can be empty. It has no leading or trailing hyphen and is at most
// 200 bytes.
func slugify(s string) string {
	var b strings.Builder
	dash := true // swallows a leading hyphen
	for _, r := range latinFold.Replace(strings.ToLower(s)) {
		if r < unicode.MaxASCII && (unicode.IsLower(r) || unicode.IsDigit(r)) {
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

// freeLocally is the slug pickLocal would choose, without recording it.
func (s *slugSet) freeLocally(schema, locale, base string) (slug string, n int) {
	for n = 1; ; n++ {
		slug = candidate(base, n)
		if !s.used[slugKey(schema, locale, slug)] {
			return slug, n
		}
	}
}

// suffixOf is the n for which candidate(base, n) is slug, 0 when none is.
func suffixOf(base, slug string) int {
	for n := 1; n <= maxSlugSuffixes; n++ {
		if candidate(base, n) == slug {
			return n
		}
	}
	return 0
}
