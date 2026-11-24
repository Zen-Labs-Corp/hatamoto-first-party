package comments

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

// Plugin templates cannot print trusted HTML, so the core-sanitised comment
// body is turned into data the section prints with normal escaping. Core's
// allowlist is p br em strong code pre blockquote and a[href, rel].

const (
	maxBlocks = 200
	maxParts  = 2000
)

var (
	tagRe  = regexp.MustCompile(`(?i)<(/?)([a-z]+)([^>]*)>`)
	hrefRe = regexp.MustCompile(`(?i)\bhref\s*=\s*"([^"]*)"`)
)

// blk is a block; exactly one of p, pre and quote is true.
func blk(kind string) map[string]any {
	return map[string]any{"p": kind == "p", "pre": kind == "pre", "quote": kind == "quote", "parts": []map[string]any{}}
}

// part is a run of text with the inline marks open at that point. Every key is
// always present so templates never meet a missing one.
func part(text, href string, em, strong, code, br bool) map[string]any {
	return map[string]any{"text": text, "href": href, "em": em, "strong": strong, "code": code, "br": br}
}

// bodyScan is the state of one bodyBlocks run.
type bodyScan struct {
	out              []map[string]any
	cur              map[string]any
	parts            int
	em, strong, code int
	href             string
	inQuote, inPre   bool
	done             bool
}

func (s *bodyScan) curParts() []map[string]any { return s.cur["parts"].([]map[string]any) }

// open starts a block of that kind, closing the current one first.
func (s *bodyScan) open(kind string) {
	s.close()
	s.cur = blk(kind)
}

// close ends the current block; empty blocks are dropped.
func (s *bodyScan) close() {
	if s.cur == nil {
		return
	}
	if len(s.curParts()) > 0 {
		if len(s.out) >= maxBlocks {
			s.done = true
		} else {
			s.out = append(s.out, s.cur)
		}
	}
	s.cur = nil
}

func (s *bodyScan) add(p map[string]any) {
	if s.parts >= maxParts {
		s.done = true
		return
	}
	if s.cur == nil {
		s.open("p")
	}
	s.cur["parts"] = append(s.curParts(), p)
	s.parts++
}

func (s *bodyScan) text(raw string) {
	if raw == "" || (s.cur == nil && strings.TrimSpace(raw) == "") {
		return
	}
	t := html.UnescapeString(raw)
	if s.inPre {
		s.add(part(t, "", false, false, false, false))
		return
	}
	s.add(part(t, s.href, s.em > 0, s.strong > 0, s.code > 0, false))
}

func (s *bodyScan) tag(closing bool, name, attrs string) {
	switch name {
	case "p":
		switch {
		case s.inQuote && !closing:
			if s.cur != nil && len(s.curParts()) > 0 {
				s.add(part("", "", false, false, false, true))
			}
		case !s.inQuote && !closing:
			s.open("p")
		case !s.inQuote:
			s.close()
		}
	case "blockquote":
		if closing {
			s.close()
			s.inQuote = false
		} else {
			s.open("quote")
			s.inQuote = true
		}
	case "pre":
		if closing {
			s.close()
			s.inPre = false
		} else {
			s.open("pre")
			s.inPre = true
		}
	case "br":
		if !closing {
			s.add(part("", "", false, false, false, true))
		}
	case "em", "strong", "code":
		d := 1
		if closing {
			d = -1
		}
		switch name {
		case "em":
			s.em = max(0, s.em+d)
		case "strong":
			s.strong = max(0, s.strong+d)
		default:
			s.code = max(0, s.code+d)
		}
	case "a":
		s.href = ""
		if !closing {
			if m := hrefRe.FindStringSubmatch(attrs); m != nil {
				s.href = safeHref(html.UnescapeString(m[1]))
			}
		}
	}
}

// safeHref keeps an absolute http(s) URL, else "".
func safeHref(h string) string {
	u, err := url.Parse(h)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return ""
	}
	return h
}

// bodyBlocks turns sanitised comment HTML into blocks of parts that the
// template escapes. Text is unescaped here and escaped again on output.
// Unknown tags are dropped and their text kept; links keep only http(s) hrefs,
// else the link text becomes plain text. Text outside any block goes into an
// implicit paragraph. At most 200 blocks and 2000 parts are produced.
func bodyBlocks(h string) []map[string]any {
	s := &bodyScan{out: []map[string]any{}}
	last := 0
	for _, m := range tagRe.FindAllStringSubmatchIndex(h, -1) {
		s.text(h[last:m[0]])
		s.tag(h[m[2]:m[3]] == "/", strings.ToLower(h[m[4]:m[5]]), h[m[6]:m[7]])
		last = m[1]
		if s.done {
			s.close()
			return s.out
		}
	}
	s.text(h[last:])
	s.close()
	return s.out
}
