package main

import (
	"encoding/json"
	"errors"
	"strings"
	"unicode/utf8"
)

// limits are the size limits of one provider request. They sit below the
// providers' documented maxima.
type limits struct{ MaxCount, MaxRequestBytes, MaxSegmentBytes int }

func limitsFor(provider string) limits {
	if provider == providerGoogle {
		return limits{MaxCount: 100, MaxRequestBytes: 60 << 10, MaxSegmentBytes: 8 << 10}
	}
	return limits{MaxCount: 50, MaxRequestBytes: 100 << 10, MaxSegmentBytes: 20 << 10}
}

// ErrUnitTooLarge reports that the first unit alone does not fit one request.
var ErrUnitTooLarge = errors.New("entry too large for one provider request")

// Cut point classes, best last.
const (
	cutWord  = iota // after white space inside text
	cutTag          // after any tag
	cutBlock        // after a block end tag, <br> or a blank line
)

type cutPoint struct{ pos, class int }

var blockEndTags = map[string]bool{
	"p": true, "div": true, "ul": true, "ol": true, "table": true,
	"blockquote": true, "li": true, "h1": true, "h2": true, "h3": true,
	"h4": true, "h5": true, "h6": true,
}

// tagCutClass classifies the end of a tag as a cut point.
func tagCutClass(tag string) int {
	if len(tag) > 2 && tag[1] == '/' {
		name := strings.ToLower(strings.TrimRight(tag[2:], "> \t\r\n"))
		if blockEndTags[name] {
			return cutBlock
		}
	} else if len(tag) > 3 && strings.EqualFold(tag[1:3], "br") {
		if c := tag[3]; c == '>' || c == '/' || c == ' ' {
			return cutBlock
		}
	}
	return cutTag
}

// cutPoints lists where s may be cut, in order, and the text ranges (where a
// hard cut at a rune boundary is allowed when nothing better exists).
func cutPoints(s string) (cuts []cutPoint, text [][2]int) {
	segs := scanHTML(s)
	pos := 0
	for i, seg := range segs {
		end := pos + len(seg.text)
		switch {
		case seg.code:
		case seg.markup:
			isOpenRaw := i+1 < len(segs) && segs[i+1].code
			if !isOpenRaw && strings.HasSuffix(seg.text, ">") {
				cuts = append(cuts, cutPoint{end, tagCutClass(seg.text)})
			}
		default:
			text = append(text, [2]int{pos, end})
			for k := 0; k < len(seg.text); k++ {
				c := seg.text[k]
				if c != ' ' && c != '\n' && c != '\t' && c != '\r' {
					continue
				}
				class := cutWord
				if c == '\n' && k > 0 && seg.text[k-1] == '\n' {
					class = cutBlock
				}
				cuts = append(cuts, cutPoint{pos + k + 1, class})
			}
		}
		pos = end
	}
	return cuts, text
}

// splitHTML cuts a fragment into segments of at most max bytes, at block
// boundaries when it can, else after a tag, else between words, and never
// inside a tag or attribute. A tag or script block longer than max is kept
// whole, so a segment can exceed max then. Joining the segments gives s back.
func splitHTML(s string, max int) []string {
	if s == "" {
		return nil
	}
	if len(s) <= max || max <= 0 {
		return []string{s}
	}
	cuts, text := cutPoints(s)
	var out []string
	start := 0
	for len(s)-start > max {
		limit := start + max
		best := -1
		bestClass := -1
		for _, c := range cuts {
			if c.pos <= start {
				continue
			}
			if c.pos > limit {
				break
			}
			if c.class >= bestClass {
				best, bestClass = c.pos, c.class
			}
		}
		if best < 0 {
			best = hardCut(s, start, limit, cuts, text)
		}
		out = append(out, s[start:best])
		start = best
	}
	return append(out, s[start:])
}

// hardCut picks a cut when no cut point lies in (start, limit]: inside a
// text run at a rune boundary, else at the next cut point (or the end).
func hardCut(s string, start, limit int, cuts []cutPoint, text [][2]int) int {
	for _, r := range text {
		if r[0] < limit && limit < r[1] {
			at := limit
			for at > start && !utf8.RuneStart(s[at]) {
				at--
			}
			if at > start {
				return at
			}
		}
	}
	for _, c := range cuts {
		if c.pos > limit {
			return c.pos
		}
	}
	return len(s)
}

// joinSegs is the inverse of splitHTML.
func joinSegs(segs []string) string { return strings.Join(segs, "") }

// firstBatch returns how many leading units fit in one provider request: at
// most MaxCount texts and MaxRequestBytes of JSON-encoded text. A unit (all
// the texts of one entity) is never split. The first unit alone not fitting
// is ErrUnitTooLarge. No units gives 0.
func firstBatch(units [][]string, l limits) (int, error) {
	count, size := 0, 1 // the enclosing [ ], less the comma after the last text
	for n, u := range units {
		add, bytes := len(u), 0
		for _, t := range u {
			b, err := json.Marshal(t)
			if err != nil {
				return 0, err
			}
			bytes += len(b) + 1 // separating comma
		}
		if count+add > l.MaxCount || size+bytes > l.MaxRequestBytes {
			if n == 0 {
				return 0, ErrUnitTooLarge
			}
			return n, nil
		}
		count += add
		size += bytes
	}
	return len(units), nil
}
