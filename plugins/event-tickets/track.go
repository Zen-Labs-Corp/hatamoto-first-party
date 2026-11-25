package main

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

// The public links of a campaign mail: the open pixel, tracked links and the
// unsubscribe page. Every token is signed; a bad one never tells a caller
// anything (the pixel answers the same image, the others 404). Nothing about
// the caller is read or stored: no address, no headers.

// pixelGIF is a 1x1 transparent GIF, 43 bytes.
var pixelGIF = []byte("GIF89a\x01\x00\x01\x00\x80\x00\x00\x00\x00\x00\xff\xff\xff!\xf9\x04\x01\x00\x00\x00\x00,\x00\x00\x00\x00\x01\x00\x01\x00\x00\x02\x02D\x01\x00;")

const (
	kindUnsubYes   = "unsub.yes"
	kindUnsubAgain = "unsub.again"
)

// serveOpen answers GET /o/{token}: it counts an open for a valid token and
// always returns the image. net/http patterns cannot hold "{token}.gif", so
// the mail's .gif suffix arrives inside the token and is stripped here.
func serveOpen(tok string) sdk.HTTPResponse {
	tok = strings.TrimSuffix(tok, ".gif")
	if key, err := hmacSecret(); err == nil {
		if code, ok := verifyToken(key, kindOpen, tok); ok {
			_ = dbExec(`UPDATE email_sends SET opened_at = coalesce(opened_at, now()), open_count = open_count + 1 WHERE code = $1 AND kind = 'campaign'`, code)
		}
	}
	return sdk.HTTPResponse{
		Status:  http.StatusOK,
		Headers: map[string]string{"Content-Type": "image/gif", "Cache-Control": "no-store"},
		BodyB64: base64.StdEncoding.EncodeToString(pixelGIF),
	}
}

var linkIdxRe = regexp.MustCompile(`^(0|1[0-9]?|[2-9])$`)

// safePath reports whether p is a path core accepts as a same-site redirect:
// one slash first, no second slash or backslash, no control characters.
func safePath(p string) bool {
	if len(p) == 0 || len(p) > 2000 || p[0] != '/' || strings.HasPrefix(p, "//") || strings.HasPrefix(p, `/\`) {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] < 0x21 || p[i] == 0x7f {
			return false
		}
	}
	return true
}

// serveClick answers GET /c/{token}?l=n. The token covers n; the target is
// the one stored for that link of the send's campaign, never taken from the
// request. A stored same-site path is a 302; any other target is shown on a
// page with one plain link, as core refuses redirects off the site.
func serveClick(tok string, q url.Values) sdk.HTTPResponse {
	l := first(q["l"])
	if !linkIdxRe.MatchString(l) {
		return notFound()
	}
	n, _ := strconv.Atoi(l)
	key, err := hmacSecret()
	if err != nil {
		return notFound()
	}
	code, ok := verifyToken(key, kindClick(n), tok)
	if !ok {
		return notFound()
	}
	var target *struct {
		URL  string  `json:"url"`
		Path *string `json:"path"`
	}
	if err := dbJSON(`SELECT coalesce((SELECT jsonb_build_object('url', l.url, 'path', l.path)
  FROM email_sends s JOIN campaign_links l ON l.campaign_id = s.ref_id AND l.idx = $2
  WHERE s.code = $1 AND s.kind = 'campaign'), 'null'::jsonb)`, &target, code, n); err != nil || target == nil {
		return notFound()
	}
	_ = dbExec(`UPDATE email_sends SET clicked_at = coalesce(clicked_at, now()), click_count = click_count + 1,
  opened_at = coalesce(opened_at, now()) WHERE code = $1`, code)
	if target.Path != nil && safePath(*target.Path) {
		return sdk.HTTPResponse{Status: http.StatusFound, Headers: map[string]string{"Location": *target.Path, "Cache-Control": "no-store"}}
	}
	if !httpURL(target.URL) {
		return notFound()
	}
	host := target.URL
	if u, err := url.Parse(target.URL); err == nil && u.Host != "" {
		host = u.Host
	}
	return htmlPage(http.StatusOK, pageData{
		Title: "You are leaving this site",
		Lines: []string{"This link goes to " + host + "."},
		Links: []pageLink{{target.URL, "Continue to " + host}},
	})
}

// serveUnsub answers GET /u/{token}. A bare token only shows a confirm page;
// the link on it carries a second signed value, and only that one writes. The
// done page carries a third value to undo it. Pages cannot hold a form (the
// route CSP is a sandbox), so the confirm is a link.
func serveUnsub(tok string, q url.Values) sdk.HTTPResponse {
	key, err := hmacSecret()
	if err != nil {
		return notFound()
	}
	code, ok := verifyToken(key, kindUnsub, tok)
	if !ok {
		return notFound()
	}
	var hash string
	if err := dbJSON(`SELECT coalesce((SELECT to_jsonb(email_hash) FROM email_sends WHERE code = $1), 'null'::jsonb)`, &hash, code); err != nil || hash == "" {
		return notFound()
	}
	base := "/p/event-tickets/u/" + tok
	if c, ok := verifyToken(key, kindUnsubYes, first(q["yes"])); ok && c == code {
		if err := dbExec(`INSERT INTO unsubscribes (email_hash) VALUES ($1) ON CONFLICT DO NOTHING`, hash); err != nil {
			return htmlPage(http.StatusServiceUnavailable, pageData{Title: "Try again later"})
		}
		return htmlPage(http.StatusOK, pageData{
			Title: "You are unsubscribed",
			Lines: []string{"You will not get more event emails from this site. Ticket confirmations still arrive."},
			Links: []pageLink{{base + "?again=" + signCode(key, kindUnsubAgain, code), "Subscribe again"}},
		})
	}
	if c, ok := verifyToken(key, kindUnsubAgain, first(q["again"])); ok && c == code {
		if err := dbExec(`DELETE FROM unsubscribes WHERE email_hash = $1`, hash); err != nil {
			return htmlPage(http.StatusServiceUnavailable, pageData{Title: "Try again later"})
		}
		return htmlPage(http.StatusOK, pageData{Title: "You are subscribed again", Lines: []string{"You will get event emails from this site again."}})
	}
	return htmlPage(http.StatusOK, pageData{
		Title: "Stop event emails from this site?",
		Lines: []string{"Ticket confirmations still arrive."},
		Links: []pageLink{{base + "?yes=" + signCode(key, kindUnsubYes, code), "Yes, unsubscribe"}},
	})
}
