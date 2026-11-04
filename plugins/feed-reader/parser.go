package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"html"
	"io"
	"net/url"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	maxParsedItems = 200
	excerptRunes   = 280
	titleRunes     = 300
	maxGUIDBytes   = 512
)

var errInvalidFeed = errors.New("invalid feed")

// parsed is a feed after parsing: its title, its site link when it has one,
// and at most 200 items.
type parsed struct {
	Title   string
	SiteURL string
	Items   []item
}

// parseFeed reads RSS 2.0, RSS 1.0 (RDF) and Atom 1.0 with the standard
// library only. It streams tokens, expands no custom entities and stops after
// 200 items. Items without a usable http(s) link are dropped; relative links
// are resolved against feedURL. Nothing in the result is HTML.
func parseFeed(body []byte, feedURL string) (parsed, error) {
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	d := xml.NewDecoder(bytes.NewReader(body))
	d.Strict = false
	d.CharsetReader = charsetReader
	base, _ := url.Parse(feedURL)

	var p parsed
	var atom, seenRoot bool
	var stack []string
	for {
		tok, err := d.Token()
		if err == io.EOF {
			if !seenRoot {
				return parsed{}, errInvalidFeed
			}
			return p, nil
		}
		if err != nil {
			if errors.Is(err, errUnsupportedCharset) {
				return parsed{}, errUnsupportedCharset
			}
			return parsed{}, errInvalidFeed
		}
		switch t := tok.(type) {
		case xml.StartElement:
			name := t.Name.Local
			parent := ""
			if len(stack) > 0 {
				parent = stack[len(stack)-1]
			}
			if !seenRoot {
				switch name {
				case "rss", "RDF":
				case "feed":
					atom = true
				default:
					return parsed{}, errInvalidFeed
				}
				seenRoot = true
			}
			isItem := (!atom && name == "item" && (parent == "channel" || parent == "RDF")) ||
				(atom && name == "entry" && parent == "feed")
			switch {
			case isItem:
				it, ok, err := parseItem(d, atom, base)
				if err != nil {
					return parsed{}, errInvalidFeed
				}
				if ok {
					p.Items = append(p.Items, it)
					if len(p.Items) == maxParsedItems {
						return p, nil
					}
				}
			case name == "title" && (parent == "channel" || (atom && parent == "feed")):
				s, err := elementText(d)
				if err != nil {
					return parsed{}, errInvalidFeed
				}
				if p.Title == "" {
					p.Title = clipRunes(cleanText(s), titleRunes)
				}
			case name == "link" && (parent == "channel" || (atom && parent == "feed")):
				var raw string
				if atom {
					href, rel := linkAttrs(t)
					if rel == "" || rel == "alternate" {
						raw = href
					}
					if err := d.Skip(); err != nil {
						return parsed{}, errInvalidFeed
					}
				} else {
					s, err := elementText(d)
					if err != nil {
						return parsed{}, errInvalidFeed
					}
					raw = s
				}
				if p.SiteURL == "" {
					p.SiteURL = resolveLink(raw, base)
				}
			default:
				stack = append(stack, name)
			}
		case xml.EndElement:
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		}
	}
}

// parseItem reads one item or entry; the decoder sits just after its start
// element and is left just after its end element. ok is false when the item
// has no usable link or guid.
func parseItem(d *xml.Decoder, atom bool, base *url.URL) (it item, ok bool, err error) {
	var title, link, guid, summary, content, published, updated string
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return item{}, false, err
		}
		switch t := tok.(type) {
		case xml.StartElement:
			if depth > 0 {
				depth++
				continue
			}
			name := t.Name.Local
			switch name {
			case "link":
				if atom {
					href, rel := linkAttrs(t)
					if link == "" && (rel == "" || rel == "alternate") {
						link = href
					}
					err = d.Skip()
				} else {
					var s string
					s, err = elementText(d)
					if link == "" {
						link = strings.TrimSpace(s)
					}
				}
			case "title", "guid", "id", "description", "summary", "content", "encoded", "pubDate", "published", "date", "updated":
				var s string
				s, err = elementText(d)
				s = strings.TrimSpace(s)
				switch {
				case s == "":
				case name == "title" && title == "":
					title = s
				case (name == "guid" || name == "id") && guid == "":
					guid = s
				case (name == "description" || name == "summary") && summary == "":
					summary = s
				case (name == "content" || name == "encoded") && content == "":
					content = s
				case (name == "pubDate" || name == "published" || name == "date") && published == "":
					published = s
				case name == "updated" && updated == "":
					updated = s
				}
			default:
				depth++
			}
			if err != nil {
				return item{}, false, err
			}
		case xml.EndElement:
			if depth > 0 {
				depth--
				continue
			}
			return buildItem(title, link, guid, summary, content, published, updated, base)
		}
	}
}

func buildItem(title, link, guid, summary, content, published, updated string, base *url.URL) (item, bool, error) {
	l := resolveLink(link, base)
	if l == "" {
		// A permalink guid stands in for a missing link.
		l = resolveLink(guid, nil)
	}
	if l == "" {
		return item{}, false, nil
	}
	if guid == "" {
		guid = l
	}
	if len(guid) > maxGUIDBytes {
		sum := sha256.Sum256([]byte(guid))
		guid = hex.EncodeToString(sum[:])
	}
	if summary == "" {
		summary = content
	}
	it := item{GUID: guid, Title: clipRunes(cleanText(title), titleRunes), Link: l, Excerpt: excerptText(summary)}
	if it.Title == "" {
		it.Title = l
	}
	if published == "" {
		published = updated
	}
	it.Published = parseDate(published)
	return it, true, nil
}

// elementText returns the character data of the element whose start tag was
// just read, including text inside nested elements, and consumes its end tag.
func elementText(d *xml.Decoder) (string, error) {
	var sb strings.Builder
	depth := 0
	for {
		tok, err := d.Token()
		if err != nil {
			return "", err
		}
		switch t := tok.(type) {
		case xml.CharData:
			sb.Write(t)
		case xml.StartElement:
			depth++
		case xml.EndElement:
			if depth == 0 {
				return sb.String(), nil
			}
			depth--
		}
	}
}

// linkAttrs returns the href and the lower-cased rel of an Atom link.
func linkAttrs(t xml.StartElement) (href, rel string) {
	for _, a := range t.Attr {
		switch a.Name.Local {
		case "href":
			href = a.Value
		case "rel":
			rel = strings.ToLower(strings.TrimSpace(a.Value))
		}
	}
	return href, rel
}

// resolveLink returns raw as an absolute http(s) URL (resolved against base
// when relative), or "" when it is anything else.
func resolveLink(raw string, base *url.URL) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	if base != nil {
		u = base.ResolveReference(u)
	}
	if (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// cleanText turns markup into plain text: tags dropped (script and style
// content with them), entities decoded, tags dropped once more in case the
// decoding revealed any, whitespace collapsed.
func cleanText(s string) string {
	s = stripTags(s)
	s = html.UnescapeString(s)
	s = stripTags(s)
	return strings.Join(strings.Fields(strings.ToValidUTF8(s, "")), " ")
}

// excerptText is cleanText cut to 280 runes plus an ellipsis.
func excerptText(s string) string {
	s = cleanText(s)
	if utf8.RuneCountInString(s) <= excerptRunes {
		return s
	}
	return strings.TrimSpace(clipRunes(s, excerptRunes)) + "…"
}

// stripTags removes HTML tags, comments and the content of script and style
// elements. A "<" only opens a tag before a letter, "/", "!" or "?", so text
// such as "1 < 2" survives. Each tag leaves a space behind.
func stripTags(s string) string {
	if !strings.ContainsRune(s, '<') {
		return s
	}
	var sb strings.Builder
	for i := 0; i < len(s); {
		c := s[i]
		if c != '<' || i+1 >= len(s) || !isTagStart(s[i+1]) {
			sb.WriteByte(c)
			i++
			continue
		}
		if strings.HasPrefix(s[i:], "<!--") {
			end := strings.Index(s[i+4:], "-->")
			if end < 0 {
				break
			}
			i += 4 + end + 3
			sb.WriteByte(' ')
			continue
		}
		end := strings.IndexByte(s[i:], '>')
		if end < 0 {
			break
		}
		name := tagName(s[i+1 : i+end])
		i += end + 1
		sb.WriteByte(' ')
		if name == "script" || name == "style" {
			if j := indexFold(s[i:], "</"+name); j >= 0 {
				i += j
			} else {
				break
			}
		}
	}
	return sb.String()
}

func isTagStart(c byte) bool {
	return c == '/' || c == '!' || c == '?' || (c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z')
}

// tagName returns the lower-cased name of an opening tag, "" for closing
// tags and declarations.
func tagName(inner string) string {
	end := 0
	for end < len(inner) && !strings.ContainsRune(" \t\r\n/", rune(inner[end])) {
		end++
	}
	return asciiLower(inner[:end])
}

func asciiLower(s string) string {
	b := []byte(s)
	for i, c := range b {
		if c >= 'A' && c <= 'Z' {
			b[i] = c + 32
		}
	}
	return string(b)
}

// indexFold finds the ASCII needle in s ignoring ASCII case. It does not
// copy s, so scanning a long text stays linear.
func indexFold(s, needle string) int {
	for i := 0; i+len(needle) <= len(s); i++ {
		if strings.EqualFold(s[i:i+len(needle)], needle) {
			return i
		}
	}
	return -1
}

// zoneOffsets replaces zone abbreviations, which time.Parse cannot resolve
// without tzdata, by numeric offsets.
var zoneOffsets = map[string]string{
	"GMT": "+0000", "UT": "+0000", "UTC": "+0000", "Z": "+0000",
	"EST": "-0500", "EDT": "-0400", "CST": "-0600", "CDT": "-0500",
	"MST": "-0700", "MDT": "-0600", "PST": "-0800", "PDT": "-0700",
}

var dateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05 -0700",
	"2006-01-02 15:04:05",
	"2006-01-02",
	"Mon, 2 Jan 2006 15:04:05 -0700",
	"Mon, 2 Jan 2006 15:04 -0700",
	"Mon, 2 Jan 06 15:04:05 -0700",
	"Mon, 2 Jan 06 15:04 -0700",
	"2 Jan 2006 15:04:05 -0700",
	"2 Jan 2006 15:04 -0700",
	"2 Jan 06 15:04 -0700",
	"Mon, 2 Jan 2006 15:04:05",
	"Mon, 2 Jan 2006",
}

// parseDate reads the date formats feeds use (RFC 822 and 1123 variants,
// RFC 3339, plain dates). The result is UTC; an unreadable date is the zero
// time.
func parseDate(s string) time.Time {
	s = strings.Join(strings.Fields(s), " ")
	if s == "" {
		return time.Time{}
	}
	if i := strings.LastIndexByte(s, ' '); i > 0 {
		if off, ok := zoneOffsets[strings.ToUpper(s[i+1:])]; ok {
			s = s[:i+1] + off
		}
	}
	for _, layout := range dateLayouts {
		if t, err := time.Parse(layout, s); err == nil {
			if t.Year() < 1 {
				return time.Time{}
			}
			return t.UTC()
		}
	}
	return time.Time{}
}
