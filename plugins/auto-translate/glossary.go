package main

import (
	"html"
	"sort"
	"strconv"
	"strings"
	"unicode"
)

// The glossary is enforced by wrapping each term, before the provider sees
// the text, in <span translate="no" data-hm-g="N">T</span> (N = index of the
// glossary entry, T = its target, or its source when the target is empty).
// Both providers leave translate="no" content alone. restore removes the
// wrappers again and puts T in, whatever the provider did inside.

const glossaryAttr = "data-hm-g"

// htmlSegment is a run of an HTML fragment: text (translatable prose) or
// markup (tags, comments, script and style content), which is never touched.
type htmlSegment struct {
	text   string
	markup bool
}

// scanHTML splits a fragment into text and markup segments by hand. A '<' that
// does not start a tag, comment or declaration is plain text. The segments
// concatenate back to the input.
func scanHTML(s string) []htmlSegment {
	var segs []htmlSegment
	add := func(t string, markup bool) {
		if t == "" {
			return
		}
		if n := len(segs); n > 0 && segs[n-1].markup == markup {
			segs[n-1].text += t
			return
		}
		segs = append(segs, htmlSegment{t, markup})
	}
	for i := 0; i < len(s); {
		if s[i] != '<' || !startsMarkup(s[i:]) {
			j := i + 1
			for j < len(s) && !(s[j] == '<' && startsMarkup(s[j:])) {
				j++
			}
			add(s[i:j], false)
			i = j
			continue
		}
		end := markupEnd(s, i)
		tag := s[i:end]
		add(tag, true)
		i = end
		if name := rawTextTag(tag); name != "" {
			// script and style content is code, not prose.
			j := indexFold(s[i:], "</"+name)
			if j < 0 {
				j = len(s) - i
			}
			add(s[i:i+j], true)
			i += j
		}
	}
	return segs
}

// startsMarkup reports whether s (which begins with '<') starts a tag, an end
// tag, a comment, a declaration or a processing instruction.
func startsMarkup(s string) bool {
	if len(s) < 2 {
		return false
	}
	c := s[1]
	return c == '/' || c == '!' || c == '?' || (c|0x20 >= 'a' && c|0x20 <= 'z')
}

// markupEnd returns the index just after the markup that starts at s[i].
// Quotes inside a tag are honoured, so a '>' in an attribute value does not
// end it. An unterminated tag runs to the end of the input.
func markupEnd(s string, i int) int {
	if strings.HasPrefix(s[i:], "<!--") {
		if j := strings.Index(s[i+4:], "-->"); j >= 0 {
			return i + 4 + j + 3
		}
		return len(s)
	}
	var quote byte
	for j := i + 1; j < len(s); j++ {
		c := s[j]
		switch {
		case quote != 0:
			if c == quote {
				quote = 0
			}
		case c == '"' || c == '\'':
			quote = c
		case c == '>':
			return j + 1
		}
	}
	return len(s)
}

// rawTextTag returns "script" or "style" when tag opens such an element.
func rawTextTag(tag string) string {
	for _, name := range []string{"script", "style"} {
		if len(tag) > len(name)+1 && strings.EqualFold(tag[1:1+len(name)], name) {
			if c := tag[1+len(name)]; c == '>' || c == '/' || c == ' ' || c == '\t' || c == '\n' || c == '\r' {
				if !strings.HasSuffix(tag, "/>") {
					return name
				}
			}
		}
	}
	return ""
}

// indexFold is strings.Index ignoring ASCII case.
func indexFold(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(sub)], sub) {
			return i
		}
	}
	return -1
}

func isWordRune(r rune) bool { return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' }

// protect wraps glossary terms found in the text nodes of an HTML fragment.
// Matching ignores case, needs a word boundary at a letter or digit edge of
// the term, prefers the longest term and never overlaps. It returns the new
// fragment and the number of wrapped terms.
func protect(fragment string, g []glossaryEntry) (string, int) {
	if len(g) == 0 || fragment == "" {
		return fragment, 0
	}
	order := make([]int, 0, len(g))
	for i, e := range g {
		if e.Source != "" {
			order = append(order, i)
		}
	}
	sort.SliceStable(order, func(a, b int) bool {
		return len([]rune(g[order[a]].Source)) > len([]rune(g[order[b]].Source))
	})
	var b strings.Builder
	total := 0
	for _, seg := range scanHTML(fragment) {
		if seg.markup {
			b.WriteString(seg.text)
			continue
		}
		out, n := protectText(seg.text, g, order)
		b.WriteString(out)
		total += n
	}
	return b.String(), total
}

func protectText(text string, g []glossaryEntry, order []int) (string, int) {
	rs := []rune(text)
	var b strings.Builder
	n := 0
	for i := 0; i < len(rs); {
		if rs[i] == '&' {
			// A character reference is not prose; skip it whole.
			if j := entityEnd(rs, i); j > i {
				b.WriteString(string(rs[i:j]))
				i = j
				continue
			}
		}
		if hit, idx := matchTerm(rs, i, g, order); hit > 0 {
			t := g[idx].Target
			if t == "" {
				t = g[idx].Source
			}
			b.WriteString(`<span translate="no" ` + glossaryAttr + `="` + strconv.Itoa(idx) + `">` + escapeText(t) + `</span>`)
			n++
			i += hit
			continue
		}
		b.WriteRune(rs[i])
		i++
	}
	return b.String(), n
}

// entityEnd returns the index after a character reference starting at rs[i]
// (&name; or &#123;), or i when there is none.
func entityEnd(rs []rune, i int) int {
	j := i + 1
	if j < len(rs) && rs[j] == '#' {
		j++
	}
	k := j
	for k < len(rs) && k-j < 32 && (rs[k] < 128 && (unicode.IsLetter(rs[k]) || unicode.IsDigit(rs[k]))) {
		k++
	}
	if k > j && k < len(rs) && rs[k] == ';' {
		return k + 1
	}
	return i
}

// matchTerm returns the length in runes of the longest term starting at
// rs[i] and its entry index, or 0.
func matchTerm(rs []rune, i int, g []glossaryEntry, order []int) (int, int) {
	for _, idx := range order {
		src := []rune(g[idx].Source)
		n := len(src)
		if i+n > len(rs) || !strings.EqualFold(string(rs[i:i+n]), g[idx].Source) {
			continue
		}
		if isWordRune(src[0]) && i > 0 && isWordRune(rs[i-1]) {
			continue
		}
		if isWordRune(src[n-1]) && i+n < len(rs) && isWordRune(rs[i+n]) {
			continue
		}
		return n, idx
	}
	return 0, 0
}

func escapeText(s string) string {
	return strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;").Replace(s)
}

// restore replaces every glossary wrapper in translated text with the term
// text (the entry's target, or its source when the target is empty). It
// tolerates any attribute order, quote style and spacing. The wrappers are
// always gone from the result. lost counts wrappers that came back broken (an
// opening tag without a closing one); a wrapper the provider dropped
// altogether cannot be seen here, see unwrap.
func restore(translated string, g []glossaryEntry) (string, int) {
	out, _, lost := unwrap(translated, g)
	return out, lost
}

// unwrap is restore that also reports how many wrappers it found.
func unwrap(s string, g []glossaryEntry) (out string, found, broken int) {
	var b strings.Builder
	for i := 0; i < len(s); {
		j := indexFold(s[i:], "<span")
		if j < 0 {
			b.WriteString(s[i:])
			break
		}
		start := i + j
		b.WriteString(s[i:start])
		end := markupEnd(s, start)
		tag := s[start:end]
		idx, ok := glossaryIndex(tag)
		if !ok {
			b.WriteString(tag)
			i = end
			continue
		}
		closeAt, closeEnd := matchingSpanClose(s, end)
		if closeAt < 0 {
			broken++
			i = end
			continue
		}
		found++
		if idx >= 0 && idx < len(g) {
			t := g[idx].Target
			if t == "" {
				t = g[idx].Source
			}
			b.WriteString(escapeText(t))
		} else {
			b.WriteString(stripWrappers(s[end:closeAt]))
		}
		i = closeEnd
	}
	return b.String(), found, broken
}

// stripWrappers removes nested glossary spans from inner text of a wrapper
// with an unknown index, keeping their content.
func stripWrappers(s string) string {
	out, _, _ := unwrap(s, nil)
	return out
}

// glossaryIndex reads data-hm-g from a <span ...> tag. ok is false when the
// tag is not a glossary wrapper.
func glossaryIndex(tag string) (idx int, ok bool) {
	if len(tag) < 6 || !strings.EqualFold(tag[:5], "<span") {
		return 0, false
	}
	if c := tag[5]; c != ' ' && c != '\t' && c != '\n' && c != '\r' && c != '>' && c != '/' {
		return 0, false
	}
	low := strings.ToLower(tag)
	for p := 0; ; {
		k := strings.Index(low[p:], glossaryAttr)
		if k < 0 {
			return 0, false
		}
		k += p
		p = k + len(glossaryAttr)
		if k == 0 || !strings.ContainsRune(" \t\r\n\"'", rune(low[k-1])) {
			continue
		}
		rest := strings.TrimLeft(tag[p:], " \t\r\n")
		if !strings.HasPrefix(rest, "=") {
			continue
		}
		rest = strings.Trim(strings.TrimLeft(rest[1:], " \t\r\n"), `"'`)
		d := 0
		for d < len(rest) && rest[d] >= '0' && rest[d] <= '9' {
			d++
		}
		n, err := strconv.Atoi(rest[:d])
		if err != nil {
			return -1, true
		}
		return n, true
	}
}

// matchingSpanClose finds the </span> that closes a span whose opening tag
// ended at from, counting nested spans. It returns -1 when there is none.
func matchingSpanClose(s string, from int) (at, end int) {
	depth := 1
	for i := from; i < len(s); {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			break
		}
		i += j
		switch {
		case len(s)-i >= 7 && strings.EqualFold(s[i:i+7], "</span>"):
			depth--
			if depth == 0 {
				return i, i + 7
			}
			i += 7
		case len(s)-i >= 6 && strings.EqualFold(s[i:i+5], "<span") && strings.ContainsRune(" \t\r\n>/", rune(s[i+5])):
			depth++
			i = markupEnd(s, i)
		default:
			i++
		}
	}
	return -1, -1
}

// plainText strips tags from translated text and unescapes entities, for
// fields that are plain text (titles, meta descriptions).
func plainText(s string) string {
	var b strings.Builder
	for _, seg := range scanHTML(s) {
		if !seg.markup {
			b.WriteString(seg.text)
		}
	}
	return html.UnescapeString(b.String())
}
