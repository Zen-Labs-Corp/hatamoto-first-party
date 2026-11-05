package main

import (
	"crypto/sha256"
	"encoding/hex"
	"strconv"
	"strings"
)

// identity holds the hashed contact details of one submission: lower-case hex
// SHA-256, empty when the value was missing or unusable. No raw value is kept.
type identity struct {
	Em       string // email, for TikTok and LinkedIn
	EmGoogle string // email in Google's format (no dots before @ for Gmail)
	Ph       string // E.164 phone including the "+", for Google and TikTok
}

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

// googleEmail applies Google's extra rule to a normalised address: dots in
// the local part of a Gmail address are removed.
func googleEmail(email string) string {
	local, domain, ok := strings.Cut(email, "@")
	if !ok || (domain != "gmail.com" && domain != "googlemail.com") {
		return email
	}
	return strings.ReplaceAll(local, ".", "") + "@" + domain
}

// digitsOf keeps the ASCII digits of s.
func digitsOf(s string) string {
	var b strings.Builder
	for _, r := range s {
		if r >= '0' && r <= '9' {
			b.WriteRune(r)
		}
	}
	return b.String()
}

// normPhone returns the phone as E.164 ("+" and 8 to 15 digits). A value that
// starts with "+" or "00" carries its country code; any other value gets
// defaultCC after its leading (trunk) zeros are dropped. Without a country
// code there is no phone.
func normPhone(v any, defaultCC string) (string, bool) {
	s, ok := text(v)
	if !ok {
		return "", false
	}
	s = strings.TrimSpace(s)
	d := digitsOf(s)
	switch {
	case strings.HasPrefix(s, "+"):
	case strings.HasPrefix(d, "00"):
		d = d[2:]
	case defaultCC != "":
		d = defaultCC + strings.TrimLeft(d, "0")
	default:
		return "", false
	}
	if len(d) < 8 || len(d) > 15 {
		return "", false
	}
	return "+" + d, true
}

// buildIdentity hashes the mapped form fields. Missing or unusable fields are
// left out; nothing here returns an error.
func buildIdentity(fields map[string]any, emailF, phoneF, defaultCC string) identity {
	var id identity
	if e, ok := normEmail(fields[emailF]); ok {
		id.Em = sha(e)
		id.EmGoogle = sha(googleEmail(e))
	}
	if p, ok := normPhone(fields[phoneF], defaultCC); ok {
		id.Ph = sha(p)
	}
	return id
}
