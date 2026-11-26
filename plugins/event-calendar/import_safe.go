package main

import (
	"encoding/json"
	"net"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// A scheduled import publishes without review while a standing approval is
// active, and core holds back the whole changeset for anything it counts as a
// link outside the declared link fields, or for a taken slug. These helpers
// keep a scheduled create inside those rules; a manual import is reviewed by
// the owner and is left as the feed has it.

const (
	maxSlugTitle = 60
	maxLinkLen   = 2048
)

var (
	spaceRunRe = regexp.MustCompile(`\S+`)
	padRe      = regexp.MustCompile(`[ \t]{2,}`)
	lineEndRe  = regexp.MustCompile(`[ \t]+\n`)
	schemeRe   = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9+.\-]*:\S`)
	mediaRe    = regexp.MustCompile(`(?i)\.(jpe?g|png|gif|webp|svg|avif|bmp|ico|tiff?|heic|mp3|mp4|m4a|m4v|mov|webm|ogg|wav|avi|mkv|pdf)$`)

	// textKeys are the free-text attributes of a created event; the link
	// fields are checked by safeLink, every other attribute is an id, a date,
	// a flag or a fixed word.
	textKeys = []string{"description", "venue_name", "venue_address", "venue_city", "price"}
	linkKeys = []string{"ticket_url", "online_url", "website"}
)

// forbiddenLinkChars may not appear in a link value.
const forbiddenLinkChars = "\"'<>\\`"

// trailingPunct is stripped from the end of a token before its file extension is read.
const trailingPunct = ".,;:!?)\"'"

// linkToken reports whether core would count one whitespace-free piece of
// text as a link.
func linkToken(tok string) bool {
	low := strings.ToLower(tok)
	switch {
	case strings.Contains(tok, "://"), strings.Contains(tok, "]("):
		return true
	case strings.HasPrefix(tok, "/"), strings.HasPrefix(tok, "\\"), strings.HasPrefix(low, "www."):
		return true
	case strings.Contains(low, "url("), strings.Contains(low, "image-set("):
		return true
	case schemeRe.MatchString(tok):
		return true
	}
	return mediaRe.MatchString(strings.TrimRight(low, trailingPunct))
}

// safeText drops the pieces of s that core counts as links. Line breaks are
// kept; the gaps the dropped pieces leave are closed.
func safeText(s string) string {
	out := spaceRunRe.ReplaceAllStringFunc(s, func(tok string) string {
		if linkToken(tok) {
			return ""
		}
		return tok
	})
	out = padRe.ReplaceAllString(out, " ")
	return strings.TrimSpace(lineEndRe.ReplaceAllString(out, "\n"))
}

var linkEscapes = strings.NewReplacer("\"", "%22", "'", "%27", "<", "%3C", ">", "%3E", "\\", "%5C", "`", "%60")

// safeLink makes a link value pass core's strict check for link fields, or
// returns "" when it cannot: https only, a dotted host name (no address, no
// port, no user), at most maxLinkLen bytes, no media file name, no second
// link. The scheme and host are lower-cased; the characters core refuses are
// percent-encoded.
func safeLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxLinkLen || strings.Contains(raw, "](") {
		return ""
	}
	u, err := url.Parse(linkEscapes.Replace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Opaque != "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	if host == "" || u.Port() != "" || strings.HasSuffix(u.Host, ":") || net.ParseIP(host) != nil ||
		!strings.Contains(host, ".") || strings.HasPrefix(host, ".") || strings.HasSuffix(host, ".") {
		return ""
	}
	u.Scheme, u.Host = "https", host
	out := u.String()
	rest := strings.TrimPrefix(out, "https://")
	if len(out) > maxLinkLen || strings.Contains(rest, "://") || mediaRe.MatchString(strings.ToLower(u.Path)) || strings.ContainsAny(out, forbiddenLinkChars) {
		return ""
	}
	return out
}

// scheduledSlug is the slug of a scheduled create: the title, the date and the
// end of the event key. A taken slug would park the whole changeset, and feeds
// repeat titles, so the slug is explicit and carries something of the key.
func scheduledSlug(title string, start time.Time, key string) string {
	base := importSlug(title)
	if len(base) > maxSlugTitle {
		base = strings.Trim(base[:maxSlugTitle], "-")
	}
	if base == "" {
		base = "event"
	}
	tail := key
	if len(tail) > 6 {
		tail = tail[len(tail)-6:]
	}
	return base + "-" + start.Format("2006-01-02") + "-" + tail
}

// safeCreate makes the attributes and the name of a scheduled create safe to
// publish by a standing approval: text without links, strict link values (a
// value that cannot qualify is dropped). It never sets an image.
func safeCreate(attrs map[string]json.RawMessage, name string) (map[string]json.RawMessage, string) {
	out := make(map[string]json.RawMessage, len(attrs))
	for k, v := range attrs {
		out[k] = v
	}
	for _, k := range textKeys {
		var s string
		if json.Unmarshal(out[k], &s) != nil {
			continue
		}
		if s = safeText(s); s == "" {
			delete(out, k)
			continue
		}
		out[k], _ = json.Marshal(s)
	}
	for _, k := range linkKeys {
		var s string
		if json.Unmarshal(out[k], &s) != nil {
			continue
		}
		if s = safeLink(s); s == "" {
			delete(out, k)
			continue
		}
		out[k], _ = json.Marshal(s)
	}
	delete(out, "image")
	if name = safeText(name); name == "" {
		name = "Untitled event"
	}
	return out, name
}
