package core

import (
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode/utf8"

	"golang.org/x/net/idna"
)

// MaxTitle is the longest story title, in characters.
const MaxTitle = 80

// CleanTitle trims a title and collapses inner whitespace; 1 to MaxTitle characters.
func CleanTitle(s string) (string, error) {
	s = strings.Join(strings.Fields(s), " ")
	if n := utf8.RuneCountInString(s); n < 1 || n > MaxTitle {
		return "", errors.New("title must be 1 to 80 characters")
	}
	return s, nil
}

// ASCIIHost lower-cases h and, when it has a non-ASCII byte, converts it to its
// punycode (IDNA lookup) form, so a unicode spelling and its xn-- form are one
// host. A pure-ASCII host is only lower-cased (no IDNA check: "_" keeps working).
// ok is false when a non-ASCII host does not convert.
func ASCIIHost(h string) (string, bool) {
	h = strings.ToLower(h)
	for i := 0; i < len(h); i++ {
		if h[i] >= 0x80 {
			a, err := idna.Lookup.ToASCII(h)
			if err != nil || a == "" {
				return "", false
			}
			return a, true
		}
	}
	return h, true
}

// NormalizeURL checks a story link (http or https, a host, no userinfo, at most
// 2000 bytes, a port, if any, of 1 to 65535) and returns it without the fragment (canon), the key two links to
// the same page share (key) and its host without a trailing "." or a leading
// "www." (domain). A non-ASCII host is stored in its punycode form.
func NormalizeURL(raw string) (canon, key, domain string, err error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2000 {
		return "", "", "", errors.New("url is too long")
	}
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return "", "", "", errors.New("url must be an http or https link")
	}
	host, hostOK := ASCIIHost(u.Hostname())
	if !hostOK {
		return "", "", "", errors.New("url must be an http or https link")
	}
	if host != strings.ToLower(u.Hostname()) {
		port := u.Port()
		u.Host = host
		if port != "" {
			u.Host += ":" + port
		}
	}
	if p := u.Port(); p != "" {
		if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 {
			return "", "", "", errors.New("url has an invalid port")
		}
	}
	u.Fragment = ""
	u.RawFragment = ""
	domain = strings.TrimPrefix(strings.TrimSuffix(host, "."), "www.")
	if domain == "" {
		return "", "", "", errors.New("url must be an http or https link")
	}
	key = domain + strings.TrimSuffix(u.EscapedPath(), "/")
	if u.RawQuery != "" {
		key += "?" + u.RawQuery
	}
	return u.String(), key, domain, nil
}

var usernameRe = regexp.MustCompile(`^[A-Za-z0-9_-]{2,15}$`)

// ValidUsername allows 2 to 15 letters, digits, "_" and "-".
func ValidUsername(s string) error {
	if !usernameRe.MatchString(s) {
		return errors.New("username must be 2 to 15 letters, digits, - or _")
	}
	return nil
}

// UsernameLower is the unique, case-insensitive form of a username.
func UsernameLower(s string) string { return strings.ToLower(s) }
