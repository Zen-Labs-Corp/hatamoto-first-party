package main

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"mime"
	"net/http"
	"net/mail"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// The public submission form. A visitor's event goes into the plugin's own
// table as pending and nothing else: no entity, no changeset. Every answer is
// generic; host and database error text never reaches the visitor.

const (
	maxSubmitBody     = 16 << 10
	maxSubmitTitle    = 200
	maxSubmitVenue    = 200
	maxSubmitDesc     = 4000
	maxSubmitEmail    = 254
	tokenMinAge       = 3 * time.Second
	tokenMaxAge       = time.Hour
	maxMailsPerHour   = 10
	submitFieldsError = "check the highlighted fields"
	submitLaterError  = "try again later"
)

// submitBody is the JSON the form posts. Website is the honeypot: people leave
// it empty, scripts that fill every field do not.
type submitBody struct {
	Title       string `json:"title"`
	Start       string `json:"start"`
	End         string `json:"end"`
	Venue       string `json:"venue"`
	Description string `json:"description"`
	Email       string `json:"email"`
	Token       string `json:"token"`
	Website     string `json:"website"`
}

func jsonReply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8"},
		Body:    string(b),
	}
}

// submitAnswer is the body of every answer; Error is set only when ok is false.
type submitAnswer struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

func submitOK() sdk.HTTPResponse { return jsonReply(http.StatusOK, submitAnswer{OK: true}) }

func submitFail(code int, msg string) sdk.HTTPResponse {
	return jsonReply(code, submitAnswer{Error: msg})
}

// tokenMAC signs the issue time of a token.
func tokenMAC(key []byte, issued string) string {
	m := hmac.New(sha256.New, key)
	m.Write([]byte(issued))
	return hex.EncodeToString(m.Sum(nil))
}

// serveSubmitToken issues the time-trap token the form sends back: the time it
// was issued, signed, so a post that comes too fast or too late is dropped.
func serveSubmitToken() sdk.HTTPResponse {
	if !loadSettings().SubmissionsEnabled {
		return plain(http.StatusNotFound, "not found")
	}
	key, err := hmacSecret()
	if err != nil {
		return submitFail(http.StatusServiceUnavailable, submitLaterError)
	}
	issued := strconv.FormatInt(now().UnixMilli(), 10)
	return jsonReply(http.StatusOK, map[string]string{"token": issued + "." + tokenMAC(key, issued)})
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

// serveSubmit takes one submitted event. Bots get the same answer as people
// ("ok") and nothing is stored; only a malformed or invalid post is told so.
func serveSubmit(r sdk.HTTPRequest) sdk.HTTPResponse {
	s := loadSettings()
	if !s.SubmissionsEnabled {
		return plain(http.StatusNotFound, "not found")
	}
	if mt, _, err := mime.ParseMediaType(r.Headers["content-type"]); err != nil || mt != "application/json" {
		return submitFail(http.StatusUnsupportedMediaType, submitFieldsError)
	}
	switch {
	case r.BodyB64 != "":
		return submitFail(http.StatusBadRequest, submitFieldsError)
	case len(r.Body) > maxSubmitBody:
		return submitFail(http.StatusRequestEntityTooLarge, submitFieldsError)
	}
	var b submitBody
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&b); err != nil || dec.Decode(&struct{}{}) != io.EOF {
		return submitFail(http.StatusBadRequest, submitFieldsError)
	}
	if strings.TrimSpace(b.Website) != "" {
		return submitOK()
	}
	key, err := hmacSecret()
	if err != nil {
		return submitFail(http.StatusServiceUnavailable, submitLaterError)
	}
	if !tokenFresh(key, b.Token) {
		return submitOK()
	}
	sub, ok := cleanSubmission(b, s.location())
	if !ok {
		return submitFail(http.StatusUnprocessableEntity, submitFieldsError)
	}
	if n, err := pendingCount(); err != nil || n >= maxSubmissionRows {
		return submitFail(http.StatusServiceUnavailable, submitLaterError)
	}
	switch err := insertSubmission(sub); {
	case errors.Is(err, errDuplicate):
		return submitOK()
	case err != nil:
		return submitFail(http.StatusServiceUnavailable, submitLaterError)
	}
	mailOwner(sub)
	return submitOK()
}

// cleanSubmission trims and caps the fields and checks them. Times are stored
// as RFC 3339, a time without an offset read in the site's zone.
func cleanSubmission(b submitBody, loc *time.Location) (submission, bool) {
	s := submission{
		Title:       capRunes(plainText(b.Title, false), maxSubmitTitle),
		Venue:       capRunes(plainText(b.Venue, false), maxSubmitVenue),
		Description: capRunes(plainText(b.Description, true), maxSubmitDesc),
		Email:       strings.TrimSpace(b.Email),
	}
	if s.Title == "" || len(s.Email) > maxSubmitEmail || !plainAddress(s.Email) {
		return s, false
	}
	start, ok := parseTime(b.Start, loc)
	if !ok {
		return s, false
	}
	s.Start = start.Format(time.RFC3339)
	if e := strings.TrimSpace(b.End); e != "" {
		end, ok := parseTime(e, loc)
		if !ok || end.Before(start) {
			return s, false
		}
		s.End = end.Format(time.RFC3339)
	}
	return s, true
}

// plainAddress reports whether s is one plain address, as mail.send needs:
// no display name, no control characters.
func plainAddress(s string) bool {
	if s == "" || strings.IndexFunc(s, unicode.IsControl) >= 0 {
		return false
	}
	a, err := mail.ParseAddress(s)
	return err == nil && a.Address == s
}

// plainText trims s and turns control characters into spaces; keepLines lets
// line breaks and tabs stay.
func plainText(s string, keepLines bool) string {
	s = strings.Map(func(r rune) rune {
		switch {
		case keepLines && (r == '\n' || r == '\t'):
			return r
		case r == '\r' && keepLines:
			return -1
		case unicode.IsControl(r):
			return ' '
		}
		return r
	}, s)
	return strings.TrimSpace(s)
}

// capRunes shortens s to at most n runes.
func capRunes(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n])
}

// notifyEmail is the address the owner wants submissions mailed to; empty when
// the setting is unset or not a plain address.
func notifyEmail() string {
	var raw struct {
		Email string `json:"notify_email"`
	}
	if callHost(sdk.FnSettingsGet, struct{}{}, &raw) != nil {
		return ""
	}
	if addr := strings.TrimSpace(raw.Email); plainAddress(addr) {
		return addr
	}
	return ""
}

// mailOwner tells the owner about a stored submission, at most ten times an
// hour; the rest wait for the hourly digest. A mail that cannot be sent is not
// an error: the submission is stored and the digest tries again.
func mailOwner(s submission) {
	to := notifyEmail()
	if to == "" {
		return
	}
	var sent int
	if dbJSON(`SELECT to_jsonb(count(*)) FROM submissions WHERE notified AND created_at > $1::timestamptz`, &sent, utcText(now().Add(-time.Hour))) != nil || sent >= maxMailsPerHour {
		return
	}
	text := strings.Join([]string{
		"Title: " + s.Title,
		"Start: " + s.Start,
		"Venue: " + s.Venue,
		"From: " + s.Email,
		"",
		"Review it in the Event calendar plugin page, Submissions tab.",
	}, "\n")
	args := map[string]any{"to": []string{to}, "subject": "New event submission: " + s.Title, "text": text}
	if callHost(sdk.FnMailSend, args, nil) != nil {
		return
	}
	_ = dbExec(`UPDATE submissions SET notified = true WHERE status = 'pending' AND lower(email) = lower($1) AND lower(title) = lower($2) AND start_at = $3`, s.Email, s.Title, s.Start)
}
