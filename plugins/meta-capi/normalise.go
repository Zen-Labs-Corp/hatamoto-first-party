package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
	"unicode"
)

// sha returns the lower-case hex SHA-256 of s.
func sha(s string) string {
	sum := sha256.Sum256([]byte(s))
	return hex.EncodeToString(sum[:])
}

// text turns a form value into a string. Strings pass as they are, numbers are
// written without an exponent; anything else (bool, list, object, null) has no
// usable text.
func text(v any) (string, bool) {
	switch x := v.(type) {
	case string:
		return x, true
	case float64:
		return strconv.FormatFloat(x, 'f', -1, 64), true
	case int:
		return strconv.Itoa(x), true
	case int64:
		return strconv.FormatInt(x, 10), true
	}
	return "", false
}

// normEmail trims and lower-cases an address; it needs exactly one "@" with
// text on both sides.
func normEmail(v any) (string, bool) {
	s, ok := text(v)
	if !ok {
		return "", false
	}
	s = strings.ToLower(strings.TrimSpace(s))
	if strings.Count(s, "@") != 1 {
		return "", false
	}
	local, domain, _ := strings.Cut(s, "@")
	if local == "" || domain == "" {
		return "", false
	}
	return s, true
}

// normPhone keeps the digits, drops leading zeros and wants at least seven.
// The country code must be in the value; none is guessed.
func normPhone(v any) (string, bool) {
	s, ok := text(v)
	if !ok {
		return "", false
	}
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	d := strings.TrimLeft(b.String(), "0")
	if len(d) < 7 {
		return "", false
	}
	return d, true
}

// normNames lower-cases a full name, removes punctuation and symbols and
// splits it into words: the first word is the first name, the last word the
// last name when there are at least two.
func normNames(v any) (first, last string) {
	s, ok := text(v)
	if !ok {
		return "", ""
	}
	clean := strings.Map(func(r rune) rune {
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			return -1
		}
		return unicode.ToLower(r)
	}, strings.TrimSpace(s))
	words := strings.Fields(clean)
	if len(words) == 0 {
		return "", ""
	}
	first = words[0]
	if len(words) >= 2 {
		last = words[len(words)-1]
	}
	return first, last
}

// userData is Meta's user_data block: hashes only, never a plain value.
type userData struct {
	Em []string `json:"em,omitempty"`
	Ph []string `json:"ph,omitempty"`
	Fn []string `json:"fn,omitempty"`
	Ln []string `json:"ln,omitempty"`
}

// buildUserData hashes the mapped form fields. Missing or unusable fields are
// left out.
func buildUserData(fields map[string]any, emailF, phoneF, nameF string) userData {
	var u userData
	if e, ok := normEmail(fields[emailF]); ok {
		u.Em = []string{sha(e)}
	}
	if p, ok := normPhone(fields[phoneF]); ok {
		u.Ph = []string{sha(p)}
	}
	fn, ln := normNames(fields[nameF])
	if fn != "" {
		u.Fn = []string{sha(fn)}
	}
	if ln != "" {
		u.Ln = []string{sha(ln)}
	}
	return u
}

// hasContact reports whether Meta has something to match on: an email or a phone.
func (u userData) hasContact() bool { return len(u.Em) > 0 || len(u.Ph) > 0 }
