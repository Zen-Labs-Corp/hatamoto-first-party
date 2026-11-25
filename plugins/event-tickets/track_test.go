package main

import (
	"encoding/base64"
	"os"
	"strings"
	"testing"
)

// trackHost fakes the send, link and unsubscribe statements on top of mailHost.
type trackHost struct {
	*mailHost
	target string // jsonb of the stored link, "null" = none
	hash   string // email_hash of the send, "" = no such send
	all    []call // every statement that reached the host
	key    []byte
}

func newTrackHost(t *testing.T) *trackHost {
	h := &trackHost{mailHost: newMailHost(t), target: `{"url":"https://other.example/a","path":null}`, hash: strings.Repeat("ab", 32)}
	inner := callHost
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		ar, _ := a["args"].([]any)
		if sql != "" {
			h.all = append(h.all, call{fn, sql, ar})
		}
		switch {
		case strings.Contains(sql, "FROM email_sends s JOIN campaign_links l"):
			return jsonInto(`{"rows":[[`+h.target+`]]}`, out)
		case strings.Contains(sql, "SELECT to_jsonb(email_hash) FROM email_sends"):
			if h.hash == "" {
				return jsonInto(`{"rows":[[null]]}`, out)
			}
			return jsonInto(`{"rows":[["`+h.hash+`"]]}`, out)
		case strings.HasPrefix(sql, "UPDATE email_sends"), strings.HasPrefix(sql, "INSERT INTO unsubscribes"), strings.HasPrefix(sql, "DELETE FROM unsubscribes"):
			return nil
		}
		return inner(fn, args, out)
	}
	k, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	h.key = k
	h.all = nil
	return h
}

// writes are the statements that change email_sends or unsubscribes.
func (h *trackHost) writes2() []call {
	var w []call
	for _, c := range h.all {
		if strings.HasPrefix(c.sql, "UPDATE email_sends") || strings.Contains(c.sql, "unsubscribes") && !strings.HasPrefix(c.sql, "SELECT") {
			w = append(w, c)
		}
	}
	return w
}

const sendCode = "0123456789abcdef0123456789abcdef"

func TestPixel(t *testing.T) {
	if len(pixelGIF) != 43 {
		t.Fatalf("pixel is %d bytes", len(pixelGIF))
	}
	h := newTrackHost(t)
	tok := signCode(h.key, kindOpen, sendCode)
	for _, in := range []string{tok, tok + ".gif"} {
		h.all = nil
		r := serveOpen(in)
		raw, err := base64.StdEncoding.DecodeString(r.BodyB64)
		if r.Status != 200 || err != nil || string(raw) != string(pixelGIF) || r.Headers["Content-Type"] != "image/gif" || r.Headers["Cache-Control"] != "no-store" {
			t.Fatalf("%s: %+v", in, r)
		}
		w := h.writes2()
		if len(w) != 1 || jsonText(w[0].args) != `["`+sendCode+`"]` {
			t.Errorf("%s: writes %+v (the code is the only argument)", in, w)
		}
	}
	// an invalid token gets the same image and counts nothing (no oracle)
	for _, bad := range []string{"nope", signCode(h.key, kindUnsub, sendCode), signCode(h.key, kindClick(0), sendCode), ""} {
		h.all = nil
		r := serveOpen(bad)
		if r.Status != 200 || r.BodyB64 == "" || len(h.writes2()) != 0 {
			t.Errorf("%q: status %d, writes %v", bad, r.Status, h.writes2())
		}
	}
}

func TestClickSameSiteRedirects(t *testing.T) {
	h := newTrackHost(t)
	h.target = `{"url":"https://example.org/events/gig?x=1","path":"/events/gig?x=1"}`
	tok := signCode(h.key, kindClick(0), sendCode)
	r := serveClick(tok, map[string][]string{"l": {"0"}})
	if r.Status != 302 || r.Headers["Location"] != "/events/gig?x=1" || r.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("%+v", r)
	}
	w := h.writes2()
	if len(w) != 1 || !strings.Contains(w[0].sql, "clicked_at = coalesce(clicked_at, now())") || !strings.Contains(w[0].sql, "opened_at = coalesce(opened_at, now())") {
		t.Errorf("writes %+v", w)
	}
}

func TestClickExternalIsAnInterstitial(t *testing.T) {
	h := newTrackHost(t)
	tok := signCode(h.key, kindClick(1), sendCode)
	r := serveClick(tok, map[string][]string{"l": {"1"}})
	if r.Status != 200 || r.Headers["Location"] != "" || strings.Count(r.Body, "https://other.example/a") != 1 || !strings.Contains(r.Body, `href="https://other.example/a"`) {
		t.Fatalf("%+v", r)
	}
	// a stored path that core would refuse never becomes a redirect
	for _, p := range []string{"//evil.example", `/\evil.example`, "/a b", "x"} {
		h.target = `{"url":"https://other.example/a","path":` + jsonText(p) + `}`
		r := serveClick(tok, map[string][]string{"l": {"1"}})
		if r.Status != 200 || r.Headers["Location"] != "" {
			t.Errorf("path %q: %+v", p, r)
		}
	}
}

func TestClickRefusals(t *testing.T) {
	h := newTrackHost(t)
	tok0 := signCode(h.key, kindClick(0), sendCode)
	cases := map[string]map[string][]string{
		"tampered l":     {"l": {"1"}}, // token is for l=0
		"no l":           {},
		"l out of range": {"l": {"20"}},
		"l not a number": {"l": {"x"}},
		"leading zero l": {"l": {"00"}},
	}
	for name, q := range cases {
		h.all = nil
		if r := serveClick(tok0, q); r.Status != 404 || r.Headers["Location"] != "" || len(h.writes2()) != 0 {
			t.Errorf("%s: %+v", name, r)
		}
	}
	h.target = `null` // unknown code
	if r := serveClick(tok0, map[string][]string{"l": {"0"}}); r.Status != 404 {
		t.Errorf("unknown code: %+v", r)
	}
	// a URL in the query is ignored: the stored target decides
	h.target = `{"url":"https://other.example/a","path":null}`
	r := serveClick(tok0, map[string][]string{"l": {"0"}, "url": {"https://evil.example"}, "to": {"/x"}})
	if r.Headers["Location"] != "" || strings.Contains(r.Body, "evil.example") || !strings.Contains(r.Body, "https://other.example/a") {
		t.Errorf("query url used: %+v", r)
	}
	// every SQL of the route takes the code and the index only
	for _, c := range h.all {
		if strings.Contains(c.sql, "evil") || strings.Contains(jsonText(c.args), "evil") {
			t.Errorf("query value reached the database: %+v", c)
		}
	}
}

func TestUnsubscribeNeedsTheSignedConfirm(t *testing.T) {
	h := newTrackHost(t)
	tok := signCode(h.key, kindUnsub, sendCode)
	for name, q := range map[string]map[string][]string{
		"bare":         {},
		"yes=1":        {"yes": {"1"}},
		"wrong kind":   {"yes": {signCode(h.key, kindUnsub, sendCode)}},
		"other code":   {"yes": {signCode(h.key, kindUnsubYes, strings.Repeat("11", 16))}},
		"again as yes": {"yes": {signCode(h.key, kindUnsubAgain, sendCode)}},
	} {
		h.all = nil
		r := serveUnsub(tok, q)
		if r.Status != 200 || len(h.writes2()) != 0 || !strings.Contains(r.Body, "Stop event emails from this site?") || !strings.Contains(r.Body, "?yes=") {
			t.Errorf("%s: status %d writes %v", name, r.Status, h.writes2())
		}
	}
	h.all = nil
	yes := signCode(h.key, kindUnsubYes, sendCode)
	r := serveUnsub(tok, map[string][]string{"yes": {yes}})
	w := h.writes2()
	if r.Status != 200 || len(w) != 1 || !strings.HasPrefix(w[0].sql, "INSERT INTO unsubscribes") || jsonText(w[0].args) != `["`+h.hash+`"]` {
		t.Fatalf("yes: %d %+v", r.Status, w)
	}
	again := signCode(h.key, kindUnsubAgain, sendCode)
	if !strings.Contains(r.Body, "You are unsubscribed") || !strings.Contains(r.Body, "?again="+again) {
		t.Errorf("done page: %s", r.Body)
	}
	h.all = nil
	r = serveUnsub(tok, map[string][]string{"again": {again}})
	w = h.writes2()
	if r.Status != 200 || len(w) != 1 || !strings.HasPrefix(w[0].sql, "DELETE FROM unsubscribes") {
		t.Fatalf("again: %d %+v", r.Status, w)
	}
	// unknown token or unknown send: 404, nothing written
	h.all = nil
	if r := serveUnsub("nope", nil); r.Status != 404 {
		t.Errorf("bad token: %d", r.Status)
	}
	h.hash = ""
	if r := serveUnsub(tok, map[string][]string{"yes": {yes}}); r.Status != 404 || len(h.writes2()) != 0 {
		t.Errorf("unknown send: %d", r.Status)
	}
}

func TestTrackStoresNothingAboutTheCaller(t *testing.T) {
	raw, err := os.ReadFile("track.go")
	if err != nil {
		t.Fatal(err)
	}
	src := string(raw)
	for _, banned := range []string{"Headers[\"User-Agent\"]", "user_agent", "Referer", "RemoteAddr", "X-Forwarded", "r.Headers"} {
		if strings.Contains(src, banned) {
			t.Errorf("track.go mentions %q", banned)
		}
	}
}

func TestJunkTokensCostNoStatement(t *testing.T) {
	h := newTrackHost(t)
	for _, tok := range []string{"x", "a", "", strings.Repeat("a", 32), strings.Repeat("g", 32) + "." + strings.Repeat("a", 32), "x.gif"} {
		serveOpen(tok)
		serveClick(tok, map[string][]string{"l": {"0"}})
		serveUnsub(tok, nil)
	}
	if len(h.all) != 0 {
		t.Errorf("%d statements for junk tokens: %+v", len(h.all), h.all)
	}
	// a well-formed token still reaches the secret
	serveOpen(strings.Repeat("a", 32) + "." + strings.Repeat("b", 32))
	if len(h.all) == 0 {
		t.Error("a well-formed token must be checked")
	}
}
