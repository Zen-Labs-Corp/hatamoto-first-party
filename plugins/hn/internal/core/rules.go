package core

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
	"unicode/utf8"
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

// NormalizeURL checks a story link (http or https, a host, at most 2000 bytes)
// and returns it without the fragment (canon), the key two links to the same
// page share (key) and its host without a leading "www." (domain).
func NormalizeURL(raw string) (canon, key, domain string, err error) {
	raw = strings.TrimSpace(raw)
	if len(raw) > 2000 {
		return "", "", "", errors.New("url is too long")
	}
	u, perr := url.Parse(raw)
	if perr != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return "", "", "", errors.New("url must be an http or https link")
	}
	u.Fragment = ""
	u.RawFragment = ""
	domain = strings.TrimPrefix(strings.ToLower(u.Hostname()), "www.")
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
