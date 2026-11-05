package main

import (
	"fmt"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// submission is what the renderers need to know about one form submission.
// Order is the form's field keys in definition order, when known.
type submission struct {
	FormSlug string
	ID       string
	Fields   map[string]any
	Order    []string
}

// view is a submission ready to render: the same content for every platform,
// not yet escaped. Link is empty when no admin address is set.
type view struct {
	Form  string
	ID    string
	Title string
	Pairs []pair
	Link  string
}

type pair struct{ Key, Val string }

// maxBody caps a rendered request body; trailing pairs are dropped to fit.
const maxBody = 20000

// buildView picks, orders, stringifies and truncates the fields to send.
func buildView(s submission, c cfg) view {
	slug := clean(s.FormSlug)
	v := view{Form: slug, ID: clean(s.ID), Title: "New submission on " + slug}
	if a := strings.TrimRight(strings.TrimSpace(c.AdminURL), "/"); a != "" && v.ID != "" {
		v.Link = a + "/settings/submissions/" + url.PathEscape(v.ID)
	}
	if !c.includeValues() {
		return v
	}
	for _, k := range pickKeys(s, c.Fields) {
		val := strings.TrimSpace(clean(stringify(s.Fields[k])))
		if val == "" {
			continue
		}
		v.Pairs = append(v.Pairs, pair{Key: cut(clean(k), 100), Val: cut(val, c.maxLen())})
	}
	return v
}

// pickKeys returns the keys to send: the chosen ones, or every key except
// those that look secret when none are chosen. Form order first, then the
// rest sorted, so the message is stable.
func pickKeys(s submission, chosen []string) []string {
	want := map[string]bool{}
	for _, k := range chosen {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	keep := func(k string) bool {
		if _, ok := s.Fields[k]; !ok {
			return false
		}
		if len(want) > 0 {
			return want[k]
		}
		l := strings.ToLower(k)
		return !strings.Contains(l, "password") && !strings.Contains(l, "token") && !strings.Contains(l, "card")
	}
	var keys []string
	seen := map[string]bool{}
	for _, k := range s.Order {
		if !seen[k] && keep(k) {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	var rest []string
	for k := range s.Fields {
		if !seen[k] && keep(k) {
			rest = append(rest, k)
		}
	}
	sort.Strings(rest)
	return append(keys, rest...)
}

// stringify turns a submitted value into text. Lists (checkbox groups) are
// joined with commas.
func stringify(v any) string {
	switch x := v.(type) {
	case nil:
		return ""
	case string:
		return x
	case bool:
		return strconv.FormatBool(x)
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64)
	case []any:
		parts := make([]string, 0, len(x))
		for _, e := range x {
			if s := stringify(e); s != "" {
				parts = append(parts, s)
			}
		}
		return strings.Join(parts, ", ")
	}
	return fmt.Sprint(v)
}

// clean drops invalid UTF-8 and control characters other than newlines.
func clean(s string) string {
	s = strings.ToValidUTF8(s, "")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\n':
			return r
		case r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// cut shortens s to n characters, ending with an ellipsis when it was cut.
// It runs before escaping so an entity is never split.
func cut(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// fit escapes raw and keeps the result within limit characters by cutting the
// raw text (never the escaped text, which could split an entity).
func fit(raw string, esc func(string) string, limit int) string {
	out := esc(raw)
	for utf8.RuneCountInString(out) > limit && raw != "" {
		rs := []rune(strings.TrimSuffix(raw, "…"))
		n := len(rs) * 9 / 10
		if n < 1 {
			return ""
		}
		raw = string(rs[:n]) + "…"
		out = esc(raw)
	}
	return out
}

// fitBody renders v and drops trailing pairs until the body fits maxBody.
func fitBody(v view, render func(view) []byte) []byte {
	for {
		b := render(v)
		if len(b) <= maxBody || len(v.Pairs) == 0 {
			return b
		}
		v.Pairs = v.Pairs[:len(v.Pairs)-1]
	}
}

// idLine is shown instead of a link when no admin address is set.
func idLine(v view) string { return "Submission #" + v.ID }
