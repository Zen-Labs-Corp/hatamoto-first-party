package main

import (
	"encoding/json"
	"fmt"
	"html"
	"net/netip"
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
	tagRe      = regexp.MustCompile(`<[^>]*>`)

	// The patterns below are copies of the ones core's create gate applies, so
	// a scheduled create is at least as strict as the gate.
	urlSchemeRe = regexp.MustCompile(`(?:^|[^a-z0-9+.-])(?:https?|ftp|ftps|ws|wss|file|data|blob|javascript|vbscript|mailto|tel|sms|about)\s*:[^\s]`)
	mediaExtRe  = regexp.MustCompile(`\.(?:jpe?g|png|gif|webp|avif|svg|bmp|ico|tiff?|heic|mp4|webm|mov|mp3|ogg|wav|pdf)\b`)
	cssImageRe  = regexp.MustCompile(`(?:url|image-set)\s*\(`)
	cssURLRe    = regexp.MustCompile(`(?:^|[^a-z0-9_-])url\s*\(`)
	dataURIRe   = regexp.MustCompile(`(?:^|[^a-z0-9_-])data:[a-z0-9]`)
	linkHostRe  = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)
	numericRe   = regexp.MustCompile(`^([0-9]+|0[xX][0-9a-fA-F]*)$`)

	// textKeys are the free-text attributes of a created event; the link
	// fields are checked by safeLink, every other attribute is an id, a date,
	// a flag or a fixed word.
	textKeys = []string{"description", "venue_name", "venue_address", "venue_city", "price"}
	linkKeys = []string{"ticket_url", "online_url", "website"}
)

// linkBannedBytes cannot appear in a link value; mediaPathMarkers are the
// paths the media library is served under.
const linkBannedBytes = "\\<>\"'`"

var mediaPathMarkers = []string{"/media/", "/uploads/", "%2fmedia%2f", "%2fuploads%2f"}

// hiddenRune reports the zero-width and bidirectional control characters core
// refuses in any text of a create.
func hiddenRune(r rune) bool {
	return r >= 0x200B && r <= 0x200D || r == 0x2060 || r == 0xFEFF ||
		r >= 0x202A && r <= 0x202E || r >= 0x2066 && r <= 0x2069
}

func stripHidden(s string) string {
	return strings.Map(func(r rune) rune {
		if hiddenRune(r) {
			return -1
		}
		return r
	}, s)
}

// decodeStable undoes HTML entities and percent escapes until nothing changes
// (at most 8 rounds), as core reads a value before it checks it.
func decodeStable(s string) string {
	for i := 0; i < 8; i++ {
		n := html.UnescapeString(s)
		if u, err := url.PathUnescape(n); err == nil {
			n = u
		}
		if n == s {
			break
		}
		s = n
	}
	return s
}

func normalizeValue(s string) string {
	return strings.ToLower(strings.ReplaceAll(decodeStable(s), `\`, "/"))
}

func looksLikeURL(l string) bool {
	t := strings.TrimSpace(l)
	return strings.Contains(t, "://") || strings.HasPrefix(t, "/") || strings.HasPrefix(t, `\`) ||
		strings.HasPrefix(t, "www.") || urlSchemeRe.MatchString(t) || mediaExtRe.MatchString(t) || cssImageRe.MatchString(t)
}

// linkLike mirrors core's LooksLikeURLOrPath and MentionsImageMarkup for text
// without markup: a link, a path, a file, a CSS url( or a data: URI.
func linkLike(s string) bool {
	l, n := strings.ToLower(s), normalizeValue(s)
	for _, f := range []string{l, n} {
		if looksLikeURL(f) || strings.Contains(f, "![") || strings.Contains(f, "](") || cssURLRe.MatchString(f) || dataURIRe.MatchString(f) {
			return true
		}
	}
	return false
}

// safeText drops the pieces of s that core counts as links, with the hidden
// characters and the markup. Line breaks are kept; the gaps the dropped pieces
// leave are closed. A result that still reads as a link as a whole (a scheme
// split from its colon by a space) is returned empty.
func safeText(s string) string {
	out, _ := scrubText(s)
	return out
}

// scrubText is safeText that also reports whether the result is clean: false
// when it still reads as a link as a whole.
func scrubText(s string) (string, bool) {
	// decoding can produce hidden characters (&#8203;, &zwj;, %E2%80%8B), so they go again after it
	s = tagRe.ReplaceAllString(stripHidden(decodeStable(stripHidden(s))), " ")
	s = strings.NewReplacer("<", " ", ">", " ").Replace(s)
	out := spaceRunRe.ReplaceAllStringFunc(s, func(tok string) string {
		if linkLike(tok) || mentionsMediaPath(tok) {
			return ""
		}
		return tok
	})
	out = padRe.ReplaceAllString(out, " ")
	out = strings.TrimSpace(lineEndRe.ReplaceAllString(out, "\n"))
	if linkLike(out) || mentionsMediaPath(out) {
		return "", false
	}
	return out, true
}

// linkHost is core's LinkHost: whether raw is one https link an automatic
// create may carry in a link field.
func linkHost(raw string) bool {
	const prefix = "https://"
	if len(raw) <= len(prefix) || len(raw) > maxLinkLen || !strings.HasPrefix(raw, prefix) {
		return false
	}
	for i := 0; i < len(raw); i++ {
		if c := raw[i]; c < 0x21 || c > 0x7e || strings.IndexByte(linkBannedBytes, c) >= 0 {
			return false
		}
	}
	rest := raw[len(prefix):]
	authority, tail := rest, ""
	if i := strings.IndexAny(rest, "/?#"); i >= 0 {
		authority, tail = rest[:i], rest[i:]
	}
	if !linkHostRe.MatchString(authority) || numericRe.MatchString(authority[strings.LastIndexByte(authority, '.')+1:]) {
		return false
	}
	if _, err := netip.ParseAddr(authority); err == nil {
		return false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Opaque != "" || u.User != nil || u.Port() != "" || u.Host != authority {
		return false
	}
	return !mediaExtRe.MatchString(strings.ToLower(tail))
}

func mentionsMediaPath(s string) bool {
	l := normalizeValue(s)
	for _, m := range mediaPathMarkers {
		if strings.Contains(l, m) || strings.HasPrefix(l, m[1:]) {
			return true
		}
	}
	return false
}

var linkEscapes = strings.NewReplacer("\"", "%22", "'", "%27", "<", "%3C", ">", "%3E", "\\", "%5C", "`", "%60")

// safeLink makes a link value pass core's strict check for link fields, or
// returns "" when it cannot: https only, an ASCII dotted host name (no address,
// no port, no user), at most maxLinkLen bytes, no media file or media library
// path, no second link. The scheme and host are lower-cased; every byte core
// refuses is percent-encoded. A link to the site's own host is not known here
// and still waits for review.
func safeLink(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxLinkLen || strings.Contains(raw, "](") {
		return ""
	}
	u, err := url.Parse(linkEscapes.Replace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.User != nil || u.Opaque != "" {
		return ""
	}
	u.Scheme, u.Host = "https", strings.ToLower(u.Host)
	var b strings.Builder
	for _, c := range []byte(u.String()) {
		if c < 0x21 || c > 0x7e {
			fmt.Fprintf(&b, "%%%02X", c)
			continue
		}
		b.WriteByte(c)
	}
	out := b.String()
	if !linkHost(out) || strings.Contains(strings.TrimPrefix(out, "https://"), "://") || mentionsMediaPath(out) {
		return ""
	}
	// core also scans link values for image markup, in the plain and the decoded form
	for _, f := range []string{strings.ToLower(out), normalizeValue(out)} {
		if strings.Contains(f, "![") || cssURLRe.MatchString(f) || dataURIRe.MatchString(f) {
			return ""
		}
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
// value that cannot qualify is dropped). It never sets an image. ok is false
// for an event whose name core would still refuse: it is not staged.
func safeCreate(attrs map[string]json.RawMessage, name string) (out map[string]json.RawMessage, newName string, ok bool) {
	out = make(map[string]json.RawMessage, len(attrs))
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
	cleaned, clean := scrubText(name)
	if !clean {
		return nil, "", false
	}
	if cleaned == "" {
		cleaned = "Untitled event"
	}
	return out, cleaned, true
}
