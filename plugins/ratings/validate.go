package main

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"
)

// maxPath caps a stored or redirected path.
const maxPath = 512

// isUUID reports the 8-4-4-4-12 hex form, either case.
func isUUID(s string) bool {
	if len(s) != 36 {
		return false
	}
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case i == 8 || i == 13 || i == 18 || i == 23:
			if c != '-' {
				return false
			}
		case c >= '0' && c <= '9', c >= 'a' && c <= 'f', c >= 'A' && c <= 'F':
		default:
			return false
		}
	}
	return true
}

// cleanPath accepts a same-site path the host will take as a Location: it
// starts with one "/", has no backslash, control, space or non-ASCII byte, no
// scheme or host, and fits maxPath. It is stricter than the host on purpose.
func cleanPath(p string) (string, bool) {
	if p == "" || len(p) > maxPath || p[0] != '/' || strings.HasPrefix(p, "//") || strings.Contains(p, "\\") {
		return "", false
	}
	for i := 0; i < len(p); i++ {
		if p[i] <= 0x20 || p[i] >= 0x7f {
			return "", false
		}
	}
	u, err := url.Parse(p)
	if err != nil || u.Scheme != "" || u.Host != "" || u.Opaque != "" || !strings.HasPrefix(u.Path, "/") {
		return "", false
	}
	return p, true
}

// newVoter returns 128 random bits as 32 lowercase hex characters.
func newVoter() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// validVoter reports a value newVoter could have produced.
func validVoter(s string) bool {
	if len(s) != 32 {
		return false
	}
	for i := 0; i < len(s); i++ {
		if c := s[i]; !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}
