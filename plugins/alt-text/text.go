package main

import (
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"
)

const (
	defaultMaxLength = 125
	maxMaxLength     = 300
)

// leadPrefixes are the lead-ins models like to add; longest first so that
// "an image showing" wins over "an image".
var leadPrefixes = buildLeadPrefixes()

func buildLeadPrefixes() []string {
	var out []string
	for _, art := range []string{"", "a ", "an ", "the "} {
		for _, noun := range []string{"image", "picture", "photo", "photograph"} {
			for _, link := range []string{"of", "showing"} {
				out = append(out, art+noun+" "+link)
			}
		}
	}
	sort.Slice(out, func(i, j int) bool { return len(out[i]) > len(out[j]) })
	return out
}

// clampMax returns the alt text length limit to use: blank or invalid gives
// the default, anything above the cap is cut to it.
func clampMax(v int) int {
	if v <= 0 {
		return defaultMaxLength
	}
	if v > maxMaxLength {
		return maxMaxLength
	}
	return v
}

// cleanAlt turns a model answer into plain alt text of at most max runes: no
// surrounding quotes, one line, no "image of" lead, cut at a word boundary,
// no trailing period.
func cleanAlt(s string, max int) string {
	const quotes = "\"'`*\u201c\u201d\u2018\u2019"
	s = strings.Join(strings.Fields(s), " ")
	s = strings.Trim(s, quotes+" ")
	s = strings.Trim(stripLead(s), quotes+" .")
	s = upperFirst(s)
	if utf8.RuneCountInString(s) > max {
		r := []rune(s)
		cut := max
		if r[max] != ' ' {
			if i := lastSpace(r[:max]); i > 0 {
				cut = i
			}
		}
		s = string(r[:cut])
	}
	return strings.TrimRight(s, " ,;:-.")
}

func stripLead(s string) string {
	for _, p := range leadPrefixes {
		if len(s) < len(p) || !strings.EqualFold(s[:len(p)], p) {
			continue
		}
		rest := s[len(p):]
		if rest != "" && rest[0] != ' ' && rest[0] != ':' && rest[0] != ',' {
			continue
		}
		return strings.TrimLeft(rest, " :,")
	}
	return s
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if n == 0 {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

func lastSpace(r []rune) int {
	for i := len(r) - 1; i >= 0; i-- {
		if r[i] == ' ' {
			return i
		}
	}
	return -1
}

// isDecorative reports whether the model answered with the DECORATIVE token
// alone (any case, optionally in quotes or with a final period). An answer
// that merely starts with the word, like "Decorative cushion on a sofa", is a
// description.
func isDecorative(s string) bool {
	s = strings.Trim(strings.TrimSpace(s), "\"'`*\u201c\u201d\u2018\u2019 ")
	s = strings.Trim(strings.TrimRight(s, ". "), "\"'`*\u201c\u201d\u2018\u2019 ")
	return strings.EqualFold(s, "DECORATIVE")
}
