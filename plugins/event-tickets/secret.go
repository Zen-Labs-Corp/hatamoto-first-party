package main

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"time"
)

const (
	secretKey   = "signing-key"
	tokenMinAge = 3 * time.Second
	tokenMaxAge = time.Hour

	kindTicket = "ticket"
	kindWait   = "wait"
	kindOrder  = "order"
	kindUnsub  = "unsub"
)

var tokenRe = regexp.MustCompile(`^[0-9a-f]{32}\.[0-9a-f]{32}$`)

// hmacSecret is the signing key of the form's time token and of the ticket
// links. It is generated on first use; two first requests at once both
// insert, one wins, both read the winner.
func hmacSecret() ([]byte, error) {
	var buf [32]byte
	if _, err := rand.Read(buf[:]); err != nil {
		return nil, err
	}
	if err := dbExec(`INSERT INTO meta (key, value) VALUES ($1, $2) ON CONFLICT (key) DO NOTHING`, secretKey, hex.EncodeToString(buf[:])); err != nil {
		return nil, err
	}
	var value string
	if err := dbJSON(`SELECT to_jsonb(value) FROM meta WHERE key = $1`, &value, secretKey); err != nil {
		return nil, err
	}
	key, err := hex.DecodeString(value)
	if err != nil || len(key) != len(buf) {
		return nil, errors.New("unusable signing key")
	}
	return key, nil
}

// tokenMAC signs the issue time of a form token.
func tokenMAC(key []byte, issued string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(issued))
	return hex.EncodeToString(m.Sum(nil))
}

// tokenFresh reports whether token was issued by this plugin between three
// seconds and an hour ago.
func tokenFresh(key []byte, token string) bool {
	issued, mac, ok := strings.Cut(token, ".")
	if !ok {
		return false
	}
	got, err := hex.DecodeString(mac)
	want, _ := hex.DecodeString(tokenMAC(key, issued))
	if err != nil || !hmac.Equal(got, want) {
		return false
	}
	ms, err := strconv.ParseInt(issued, 10, 64)
	if err != nil {
		return false
	}
	age := now().Sub(time.UnixMilli(ms))
	return age >= tokenMinAge && age <= tokenMaxAge
}

// newCode is a 128-bit random code as 32 lowercase hex characters.
func newCode() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func codeMAC(key []byte, kind, code string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(kind + ":" + code))
	return hex.EncodeToString(m.Sum(nil))[:32]
}

// signCode turns a code into the token put in links: code, a dot and its
// signature. kind (ticket, wait) keeps a token of one kind from passing as
// the other.
func signCode(key []byte, kind, code string) string {
	return code + "." + codeMAC(key, kind, code)
}

// verifyToken returns the code of a token signCode made, and false for any
// other string.
func verifyToken(key []byte, kind, tok string) (string, bool) {
	if !tokenRe.MatchString(tok) {
		return "", false
	}
	code, mac, _ := strings.Cut(tok, ".")
	if !hmac.Equal([]byte(mac), []byte(codeMAC(key, kind, code))) {
		return "", false
	}
	return code, true
}
