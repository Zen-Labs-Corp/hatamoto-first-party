package main

import (
	"html"
	"net/url"
	"regexp"
	"strings"
)

// A campaign body is parsed into pieces and written out again from fixed tag
// strings and escaped text. Nothing the owner typed reaches the mail as markup:
// the tags, their order and the one attribute (a checked http or https href)
// are all produced here.

const (
	maxBodyBytesCampaign = 8000
	maxCampaignLinks     = 20
	maxLinkLen           = 2000
)

// piece is a tag or a run of text. Name is empty for text. Link is the index
// of the target of an opening a.
type piece struct {
	Name  string
	Close bool
	Text  string
	Link  int
}

// body is a parsed campaign body: the pieces and the distinct links, in order
// of first use.
type body struct {
	Pieces []piece
	Links  []string
}

var (
	openTags  = map[string]string{"p": "<p>", "br": "<br>", "b": "<b>", "strong": "<strong>", "i": "<i>", "em": "<em>", "ul": "<ul>", "ol": "<ol>", "li": "<li>", "h2": "<h2>", "h3": "<h3>"}
	closeTags = map[string]string{"p": "</p>", "b": "</b>", "strong": "</strong>", "i": "</i>", "em": "</em>", "ul": "</ul>", "ol": "</ol>", "li": "</li>", "h2": "</h2>", "h3": "</h3>"}

	// elements whose content is never shown
	skipTags = map[string]bool{"script": true, "style": true, "iframe": true, "object": true, "template": true, "noscript": true, "title": true, "head": true}

	htmlToken   = regexp.MustCompile(`<!--[\s\S]*?-->|<[^>]*>|[^<]+|<`)
	tagName     = regexp.MustCompile(`^<\s*(/?)\s*([A-Za-z][A-Za-z0-9]*)`)
	attrPattern = regexp.MustCompile(`([A-Za-z_:][-A-Za-z0-9_:.]*)(?:\s*=\s*("[^"]*"|'[^']*'|[^\s"'>]+))?`)
	spaceRun    = regexp.MustCompile(`\s+`)
	bareURL     = regexp.MustCompile(`https?://[^\s<>"']+`)
	paraSplit   = regexp.MustCompile(`\n[ \t]*\n+`)
)

// checkLink returns the cleaned URL when raw is an absolute http or https URL.
func checkLink(raw string) (string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxLinkLen || strings.ContainsFunc(raw, func(r rune) bool { return r <= ' ' || r == 0x7f }) {
		return "", false
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return "", false
	}
	return raw, true
}

// parseBody reads an owner's text or html into a body. The error is a short
// message for the owner, empty when the body is fine.
func parseBody(format, src string) (body, string) {
	if len(src) > maxBodyBytesCampaign {
		return body{}, "too long"
	}
	src = strings.ReplaceAll(src, "\r\n", "\n")
	src = plainText(src, true)
	if src == "" {
		return body{}, "empty"
	}
	var p parser
	switch format {
	case "text":
		p.parseText(src)
	case "html":
		p.parseHTML(src)
	default:
		return body{}, "bad format"
	}
	if p.err != "" {
		return body{}, p.err
	}
	return p.body, ""
}

type parser struct {
	body
	stack []string
	err   string
}

func (p *parser) text(s string) {
	if s != "" {
		p.Pieces = append(p.Pieces, piece{Text: s})
	}
}

func (p *parser) open(name string) {
	p.Pieces = append(p.Pieces, piece{Name: name, Link: -1})
	if name != "br" {
		p.stack = append(p.stack, name)
	}
}

// closeTo closes the nearest open element called name and everything inside
// it; a closing tag with no open partner is ignored.
func (p *parser) closeTo(name string) {
	for i := len(p.stack) - 1; i >= 0; i-- {
		if p.stack[i] != name {
			continue
		}
		for j := len(p.stack) - 1; j >= i; j-- {
			p.Pieces = append(p.Pieces, piece{Name: p.stack[j], Close: true})
		}
		p.stack = p.stack[:i]
		return
	}
}

func (p *parser) inAnchor() bool {
	for _, n := range p.stack {
		if n == "a" {
			return true
		}
	}
	return false
}

// link registers a target and returns its index; -1 when there are too many.
func (p *parser) link(u string) int {
	for i, l := range p.Links {
		if l == u {
			return i
		}
	}
	if len(p.Links) >= maxCampaignLinks {
		p.err = "too many links"
		return -1
	}
	p.Links = append(p.Links, u)
	return len(p.Links) - 1
}

func (p *parser) openAnchor(u string) {
	if i := p.link(u); i >= 0 {
		p.Pieces = append(p.Pieces, piece{Name: "a", Link: i})
		p.stack = append(p.stack, "a")
	}
}

func (p *parser) parseText(src string) {
	for _, para := range paraSplit.Split(strings.TrimSpace(src), -1) {
		if strings.TrimSpace(para) == "" {
			continue
		}
		p.open("p")
		for i, line := range strings.Split(para, "\n") {
			if i > 0 {
				p.open("br")
			}
			p.textLine(strings.TrimSpace(line))
		}
		p.closeTo("p")
	}
}

// textLine writes a line, turning bare http(s) addresses into links. An
// address that does not check out stays plain text.
func (p *parser) textLine(line string) {
	for line != "" && p.err == "" {
		loc := bareURL.FindStringIndex(line)
		if loc == nil {
			p.text(line)
			return
		}
		p.text(line[:loc[0]])
		raw := strings.TrimRight(line[loc[0]:loc[1]], `.,;:!?)"'`)
		rest := line[loc[0]+len(raw):]
		if u, ok := checkLink(raw); ok {
			p.openAnchor(u)
			p.text(raw)
			p.closeTo("a")
		} else {
			p.text(raw)
		}
		line = rest
	}
}

func (p *parser) parseHTML(src string) {
	skip, depth := "", 0
	for _, tok := range htmlToken.FindAllString(src, -1) {
		if p.err != "" {
			return
		}
		isTag := strings.HasPrefix(tok, "<") && strings.HasSuffix(tok, ">") && len(tok) > 1 && !strings.HasPrefix(tok, "<!--")
		var name string
		var closing bool
		if isTag {
			if m := tagName.FindStringSubmatch(tok); m != nil {
				closing, name = m[1] == "/", strings.ToLower(m[2])
			}
		}
		if skip != "" {
			switch {
			case name == skip && closing:
				if depth--; depth == 0 {
					skip = ""
				}
			case name == skip && !strings.HasSuffix(tok, "/>"):
				depth++
			}
			continue
		}
		switch {
		case strings.HasPrefix(tok, "<!--"):
		case !isTag:
			if tok == "<" {
				p.text("<")
			} else {
				p.text(spaceRun.ReplaceAllString(html.UnescapeString(tok), " "))
			}
		case name == "":
		case skipTags[name] && !closing:
			if !strings.HasSuffix(tok, "/>") {
				skip, depth = name, 1
			}
		case name == "br" && !closing:
			p.open("br")
		case name == "a":
			if closing {
				p.closeTo("a")
			} else if !p.inAnchor() {
				p.anchor(tok)
			}
		case closing:
			if _, ok := closeTags[name]; ok {
				p.closeTo(name)
			}
		default:
			if _, ok := closeTags[name]; ok {
				p.open(name)
			}
		}
	}
	for len(p.stack) > 0 && p.err == "" {
		p.closeTo(p.stack[0])
	}
}

// anchor opens a link from the href of an a tag. A link whose address is not
// http or https refuses the whole body; an a without href is dropped.
func (p *parser) anchor(tok string) {
	attrs := tagName.ReplaceAllString(tok, "")
	for _, m := range attrPattern.FindAllStringSubmatch(attrs, -1) {
		if !strings.EqualFold(m[1], "href") {
			continue
		}
		val := strings.Trim(m[2], `"'`)
		u, ok := checkLink(html.UnescapeString(val))
		if !ok {
			p.err = "bad link"
			return
		}
		p.openAnchor(u)
		return
	}
}

// html writes the body as mail markup; href maps a link index to the address
// to put in the mail (a tracked address for campaigns).
func (b body) html(href func(i int) string) string {
	var s strings.Builder
	for _, pc := range b.Pieces {
		switch {
		case pc.Name == "":
			s.WriteString(html.EscapeString(pc.Text))
		case pc.Name == "a" && pc.Close:
			s.WriteString("</a>")
		case pc.Name == "a":
			s.WriteString(`<a href="` + html.EscapeString(href(pc.Link)) + `">`)
		case pc.Close:
			s.WriteString(closeTags[pc.Name])
		default:
			s.WriteString(openTags[pc.Name])
		}
	}
	return s.String()
}

var (
	blankRuns  = regexp.MustCompile(`\n{3,}`)
	lineSpaces = regexp.MustCompile(`[ \t]*\n[ \t]*`)
)

// text writes the body as plain text; a link shows its address in brackets,
// and a link whose text is the address is just the address.
func (b body) text(href func(i int) string) string {
	var s, linkText strings.Builder
	link, start := -1, 0
	for _, pc := range b.Pieces {
		switch {
		case pc.Name == "":
			s.WriteString(pc.Text)
			if link >= 0 {
				linkText.WriteString(pc.Text)
			}
		case pc.Name == "a" && !pc.Close:
			link, start = pc.Link, s.Len()
			linkText.Reset()
		case pc.Name == "a":
			h := href(link)
			if strings.TrimSpace(linkText.String()) == b.Links[link] {
				out := s.String()[:start]
				s.Reset()
				s.WriteString(out + h)
			} else {
				s.WriteString(" (" + h + ")")
			}
			link = -1
		case pc.Name == "br" && !pc.Close:
			s.WriteString("\n")
		case pc.Name == "li" && !pc.Close:
			s.WriteString("- ")
		case pc.Name == "li", pc.Name == "ul" && pc.Close, pc.Name == "ol" && pc.Close:
			s.WriteString("\n")
		case pc.Close && (pc.Name == "p" || pc.Name == "h2" || pc.Name == "h3"):
			s.WriteString("\n\n")
		}
	}
	return strings.TrimSpace(blankRuns.ReplaceAllString(lineSpaces.ReplaceAllString(s.String(), "\n"), "\n\n"))
}
