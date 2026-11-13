package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

// submitHost fakes what the submit route reads and writes: settings, the
// signing key, the submissions table (as a count and the inserted rows) and
// the mail call. Any entity or changeset call fails the test.
type submitHost struct {
	notify    string
	pending   int
	dupe      bool
	dbDown    bool
	mailErr   error
	mailed    int // notified rows in the last hour
	key       string
	inserted  [][]any
	mails     []map[string]any
	marked    int
	entityOps int
}

func (h *submitHost) install(t *testing.T) {
	t.Helper()
	base := time.Date(2027, 1, 1, 12, 0, 0, 0, time.UTC)
	now = func() time.Time { return base }
	t.Cleanup(func() { now = time.Now })
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		switch {
		case fn == sdk.FnSettingsGet:
			return json.Unmarshal([]byte(`{"notify_email":"`+h.notify+`","time_zone":"UTC"}`), out)
		case fn == sdk.FnMailSend:
			h.mails = append(h.mails, a)
			return h.mailErr
		case strings.HasPrefix(fn, "entities.") || strings.HasPrefix(fn, "changeset."):
			h.entityOps++
			t.Errorf("unexpected host call %q", fn)
			return nil
		case h.dbDown:
			return errors.New("db down")
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if h.key == "" {
				h.key = a["args"].([]any)[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return json.Unmarshal([]byte(`{"rows":[["`+h.key+`"]]}`), out)
		case strings.Contains(sql, "status = 'pending'") && strings.HasPrefix(sql, "SELECT"):
			return json.Unmarshal([]byte(`{"rows":[[`+jsonText(h.pending)+`]]}`), out)
		case strings.Contains(sql, "WHERE notified AND"):
			return json.Unmarshal([]byte(`{"rows":[[`+jsonText(h.mailed)+`]]}`), out)
		case strings.HasPrefix(sql, "INSERT INTO submissions"):
			n := 1
			if h.dupe {
				n = 0
			} else {
				h.inserted = append(h.inserted, a["args"].([]any))
			}
			return json.Unmarshal([]byte(`{"rows_affected":`+jsonText(n)+`}`), out)
		case strings.HasPrefix(sql, "UPDATE submissions SET notified"):
			h.marked++
			return nil
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
}

// token issues a token and moves the clock by wait.
func (h *submitHost) token(t *testing.T, wait time.Duration) string {
	t.Helper()
	r := serveSubmitToken()
	var out struct{ Token string }
	if r.Status != 200 || json.Unmarshal([]byte(r.Body), &out) != nil || out.Token == "" {
		t.Fatalf("token answer %d %s", r.Status, r.Body)
	}
	at := now()
	now = func() time.Time { return at.Add(wait) }
	return out.Token
}

func post(body any) sdk.HTTPRequest {
	b, _ := body.(string)
	if b == "" {
		raw, _ := json.Marshal(body)
		b = string(raw)
	}
	return sdk.HTTPRequest{Method: "POST", Pattern: "/submit", Headers: map[string]string{"content-type": "application/json"}, Body: b}
}

func goodBody(token string) map[string]any {
	return map[string]any{
		"title": "Open mic night", "start": "2027-02-20T20:00", "end": "2027-02-20T22:00",
		"venue": "The Anchor", "description": "Bring a song.", "email": "visitor@example.org",
		"token": token, "website": "",
	}
}

func answer(t *testing.T, r sdk.HTTPResponse) submitAnswer {
	t.Helper()
	var a submitAnswer
	if err := json.Unmarshal([]byte(r.Body), &a); err != nil {
		t.Fatalf("answer %d %q: %v", r.Status, r.Body, err)
	}
	return a
}

func TestSubmitHoneypot(t *testing.T) {
	h := &submitHost{notify: "owner@example.org"}
	h.install(t)
	b := goodBody(h.token(t, 5*time.Second))
	b["website"] = "https://example.com"
	r := serveSubmit(post(b))
	if r.Status != 200 || !answer(t, r).OK {
		t.Errorf("answer %d %s", r.Status, r.Body)
	}
	if len(h.inserted) != 0 || len(h.mails) != 0 {
		t.Errorf("a honeypot post stored %v or mailed %v", h.inserted, h.mails)
	}
}

func TestSubmitTimeTrap(t *testing.T) {
	cases := []struct {
		name   string
		wait   time.Duration
		mutate func(string) string
		stored bool
	}{
		{"fresh enough", 4 * time.Second, nil, true},
		{"too fresh", time.Second, nil, false},
		{"expired", time.Hour + time.Second, nil, false},
		{"last minute", time.Hour, nil, true},
		{"forged mac", 10 * time.Second, func(s string) string {
			if strings.HasSuffix(s, "0") {
				return s[:len(s)-1] + "1"
			}
			return s[:len(s)-1] + "0"
		}, false},
		{"forged time", 10 * time.Second, func(s string) string { return "1." + strings.SplitN(s, ".", 2)[1] }, false},
		{"malformed", 10 * time.Second, func(string) string { return "nonsense" }, false},
		{"empty", 10 * time.Second, func(string) string { return "" }, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := &submitHost{}
			h.install(t)
			tok := h.token(t, c.wait)
			if c.mutate != nil {
				tok = c.mutate(tok)
			}
			r := serveSubmit(post(goodBody(tok)))
			if r.Status != 200 || !answer(t, r).OK {
				t.Errorf("answer %d %s: a dropped post answers like an accepted one", r.Status, r.Body)
			}
			if got := len(h.inserted) == 1; got != c.stored {
				t.Errorf("stored = %v, want %v", got, c.stored)
			}
		})
	}
}

func TestSubmitPendingCap(t *testing.T) {
	h := &submitHost{pending: maxSubmissionRows}
	h.install(t)
	r := serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
	if r.Status != 503 || answer(t, r).Error != submitLaterError || len(h.inserted) != 0 {
		t.Errorf("answer %d %s, stored %v", r.Status, r.Body, h.inserted)
	}
	h2 := &submitHost{pending: maxSubmissionRows - 1}
	h2.install(t)
	if r := serveSubmit(post(goodBody(h2.token(t, 5*time.Second)))); r.Status != 200 || len(h2.inserted) != 1 {
		t.Errorf("one below the cap: %d %s", r.Status, r.Body)
	}
}

func TestSubmitValidation(t *testing.T) {
	h := &submitHost{}
	h.install(t)
	tok := h.token(t, 5*time.Second)

	bad := map[string]func(map[string]any){
		"no title":      func(b map[string]any) { b["title"] = "  " },
		"no start":      func(b map[string]any) { b["start"] = "" },
		"bad start":     func(b map[string]any) { b["start"] = "next friday" },
		"bad end":       func(b map[string]any) { b["end"] = "soon" },
		"end < start":   func(b map[string]any) { b["end"] = "2027-02-20T19:00" },
		"bad email":     func(b map[string]any) { b["email"] = "visitor" },
		"display name":  func(b map[string]any) { b["email"] = "Visitor <visitor@example.org>" },
		"crlf email":    func(b map[string]any) { b["email"] = "visitor@example.org\r\nBcc: x@example.com" },
		"two addresses": func(b map[string]any) { b["email"] = "a@example.org, b@example.org" },
		"long email":    func(b map[string]any) { b["email"] = strings.Repeat("a", 250) + "@example.org" },
	}
	for name, mut := range bad {
		t.Run(name, func(t *testing.T) {
			b := goodBody(tok)
			mut(b)
			r := serveSubmit(post(b))
			if r.Status != 422 || answer(t, r) != (submitAnswer{Error: submitFieldsError}) {
				t.Errorf("answer %d %s", r.Status, r.Body)
			}
		})
	}
	if len(h.inserted) != 0 {
		t.Fatalf("invalid posts stored %v", h.inserted)
	}

	t.Run("not json type", func(t *testing.T) {
		for _, ct := range []string{"application/x-www-form-urlencoded", "text/plain", ""} {
			req := post(goodBody(tok))
			req.Headers = map[string]string{"content-type": ct}
			if r := serveSubmit(req); r.Status != 415 {
				t.Errorf("%q: status %d", ct, r.Status)
			}
		}
		req := post(goodBody(tok))
		req.Headers = map[string]string{"content-type": "application/json; charset=utf-8"}
		if r := serveSubmit(req); r.Status != 200 {
			t.Errorf("charset parameter: status %d", r.Status)
		}
	})
	t.Run("too large", func(t *testing.T) {
		b := goodBody(tok)
		b["description"] = strings.Repeat("a", maxSubmitBody)
		if r := serveSubmit(post(b)); r.Status != 413 {
			t.Errorf("status %d", r.Status)
		}
	})
	t.Run("body_b64", func(t *testing.T) {
		req := post(goodBody(tok))
		req.BodyB64, req.Body = "e30=", ""
		if r := serveSubmit(req); r.Status != 400 {
			t.Errorf("status %d", r.Status)
		}
	})
	t.Run("unknown key and junk", func(t *testing.T) {
		b := goodBody(tok)
		b["role"] = "owner"
		for _, body := range []any{b, `not json`, `{"title":1}`, `[]`, `{}{}`, ``} {
			if r := serveSubmit(post(body)); r.Status != 400 || answer(t, r).Error != submitFieldsError {
				t.Errorf("%v: answer %d %s", body, r.Status, r.Body)
			}
		}
	})
	t.Run("caps and cleaning", func(t *testing.T) {
		before := len(h.inserted)
		b := goodBody(tok)
		b["title"] = "Tab\x00le " + strings.Repeat("é", 300)
		b["venue"] = strings.Repeat("é", 300)
		b["description"] = strings.Repeat("é", 4100)
		b["end"] = ""
		if r := serveSubmit(post(b)); r.Status != 200 {
			t.Fatalf("status %d %s", r.Status, r.Body)
		}
		row := h.inserted[before]
		if got := []int{len([]rune(row[0].(string))), len([]rune(row[3].(string))), len([]rune(row[4].(string)))}; got[0] != maxSubmitTitle || got[1] != maxSubmitVenue || got[2] != maxSubmitDesc {
			t.Errorf("rune lengths %v", got)
		}
		if !strings.HasPrefix(row[0].(string), "Tab le ") || row[1] != "2027-02-20T20:00:00Z" || row[2] != "" {
			t.Errorf("row %v", row[:3])
		}
	})
}

func TestSubmitStoresPendingOnly(t *testing.T) {
	h := &submitHost{notify: "owner@example.org"}
	h.install(t)
	r := serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
	if r.Status != 200 || !answer(t, r).OK {
		t.Fatalf("answer %d %s", r.Status, r.Body)
	}
	if len(h.inserted) != 1 || h.entityOps != 0 {
		t.Fatalf("inserted %v, entity ops %d", h.inserted, h.entityOps)
	}
	row := h.inserted[0]
	if row[0] != "Open mic night" || row[1] != "2027-02-20T20:00:00Z" || row[2] != "2027-02-20T22:00:00Z" || row[5] != "visitor@example.org" {
		t.Errorf("row %v", row)
	}
	if strings.Contains(r.Body, "@") {
		t.Errorf("the answer carries the address: %s", r.Body)
	}
	if len(h.mails) != 1 {
		t.Fatalf("mails %v", h.mails)
	}
	m := h.mails[0]
	text := m["text"].(string)
	if m["subject"] != "New event submission: Open mic night" || !strings.Contains(text, "visitor@example.org") || !strings.Contains(text, "Submissions tab") {
		t.Errorf("mail %v", m)
	}
	if h.marked != 1 {
		t.Errorf("the submission was not marked as mailed (%d)", h.marked)
	}
}

func TestSubmitMailFailOpen(t *testing.T) {
	for name, err := range map[string]error{
		"send error":  errors.New("smtp down"),
		"unavailable": &sdk.HostError{Code: sdk.ErrUnavailable, Message: "mail is not configured at smtp.example.com"},
	} {
		t.Run(name, func(t *testing.T) {
			h := &submitHost{notify: "owner@example.org", mailErr: err}
			h.install(t)
			r := serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
			if r.Status != 200 || !answer(t, r).OK || len(h.inserted) != 1 {
				t.Errorf("answer %d %s", r.Status, r.Body)
			}
			if strings.Contains(r.Body, "smtp") || h.marked != 0 {
				t.Errorf("body %s, marked %d", r.Body, h.marked)
			}
		})
	}
	t.Run("no notify address", func(t *testing.T) {
		h := &submitHost{}
		h.install(t)
		serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
		if len(h.mails) != 0 || len(h.inserted) != 1 {
			t.Errorf("mails %v inserted %v", h.mails, h.inserted)
		}
	})
	t.Run("over the hourly mail cap", func(t *testing.T) {
		h := &submitHost{notify: "owner@example.org", mailed: maxMailsPerHour}
		h.install(t)
		serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
		if len(h.mails) != 0 || len(h.inserted) != 1 || h.marked != 0 {
			t.Errorf("mails %v inserted %v marked %d: the digest sends this one", h.mails, h.inserted, h.marked)
		}
	})
	t.Run("subject has no control characters", func(t *testing.T) {
		h := &submitHost{notify: "owner@example.org"}
		h.install(t)
		b := goodBody(h.token(t, 5*time.Second))
		b["title"] = "Quiz\r\nBcc: x@example.com"
		serveSubmit(post(b))
		if s := h.mails[0]["subject"].(string); strings.ContainsAny(s, "\r\n") {
			t.Errorf("subject %q", s)
		}
	})
}

func TestSubmitSilentDuplicate(t *testing.T) {
	h := &submitHost{notify: "owner@example.org", dupe: true}
	h.install(t)
	r := serveSubmit(post(goodBody(h.token(t, 5*time.Second))))
	if r.Status != 200 || !answer(t, r).OK || len(h.mails) != 0 {
		t.Errorf("answer %d %s, mails %v", r.Status, r.Body, h.mails)
	}
}

func TestSubmitDatabaseDown(t *testing.T) {
	h := &submitHost{dbDown: true}
	h.install(t)
	if r := serveSubmitToken(); r.Status != 503 || strings.Contains(r.Body, "down") {
		t.Errorf("token answer %d %s", r.Status, r.Body)
	}
	r := serveSubmit(post(goodBody("1.ab")))
	if r.Status != 503 || strings.Contains(r.Body, "down") || answer(t, r).Error != submitLaterError {
		t.Errorf("answer %d %s", r.Status, r.Body)
	}
}
