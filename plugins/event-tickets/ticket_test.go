package main

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"image/png"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// ticketHost fakes the signing key, the ticket lookup and the event read.
type ticketHost struct {
	key       string
	ticket    string // jsonb cell of the lookup; "null" = unknown or anonymised
	entity    string
	entityErr error
	lookups   []string
}

const holderRow = `{"status":"active","event_id":"e1","holder":"Ana <b>","type_name":"Standard","event_name":"Gig (stored)"}`

func newTicketHost(t *testing.T) *ticketHost {
	h := &ticketHost{ticket: holderRow, entity: gigEntity}
	callHost = func(fn string, args, out any) error {
		a, _ := args.(map[string]any)
		sql, _ := a["sql"].(string)
		switch {
		case fn == sdk.FnSettingsGet:
			return jsonInto(`{}`, out)
		case fn == sdk.FnEntitiesGet:
			if h.entityErr != nil {
				return h.entityErr
			}
			return jsonInto(h.entity, out)
		case strings.HasPrefix(sql, "INSERT INTO meta"):
			if h.key == "" {
				h.key = a["args"].([]any)[1].(string)
			}
			return nil
		case strings.Contains(sql, "FROM meta"):
			return jsonInto(`{"rows":[["`+h.key+`"]]}`, out)
		case strings.Contains(sql, "FROM attendees a"):
			h.lookups = append(h.lookups, a["args"].([]any)[0].(string))
			return jsonInto(`{"rows":[[`+h.ticket+`]]}`, out)
		}
		t.Errorf("unexpected host call %q %q", fn, sql)
		return nil
	}
	return h
}

// token signs a fresh ticket token for code.
func (h *ticketHost) token(t *testing.T, kind string) string {
	t.Helper()
	if _, err := hmacSecret(); err != nil {
		t.Fatal(err)
	}
	key, err := hmacSecret()
	if err != nil {
		t.Fatal(err)
	}
	return signCode(key, kind, "0123456789abcdef0123456789abcdef")
}

func publicGet(t *testing.T, pattern string, values map[string]string, query map[string][]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: "GET", Pattern: pattern, PathValues: values, Query: query})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	if err := json.Unmarshal(out, &resp); err != nil {
		t.Fatal(err)
	}
	return resp
}

func getTicket(t *testing.T, tok string) sdk.HTTPResponse {
	return publicGet(t, "/ticket/{token}", map[string]string{"token": tok}, nil)
}

func flipTok(tok string) string {
	return tok[:len(tok)-1] + string(flipHex(tok[len(tok)-1]))
}

func TestTicketPage(t *testing.T) {
	h := newTicketHost(t)
	r := getTicket(t, h.token(t, kindTicket))
	if r.Status != 200 {
		t.Fatalf("status %d", r.Status)
	}
	if !strings.HasPrefix(r.Headers["Content-Type"], "text/html") || r.Headers["Cache-Control"] != "no-store, private" {
		t.Errorf("headers %v", r.Headers)
	}
	for _, want := range []string{"01234567 89abcdef 01234567 89abcdef", "data:image/png;base64,", "Gig", "Standard", "Ana &lt;b&gt;", "Active"} {
		if !strings.Contains(r.Body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
	for _, bad := range []string{"<script", "<form", "<b>"} {
		if strings.Contains(r.Body, bad) {
			t.Errorf("page contains %q", bad)
		}
	}
	if len(h.lookups) != 1 || h.lookups[0] != "0123456789abcdef0123456789abcdef" {
		t.Errorf("lookups %v", h.lookups)
	}
}

func TestTicketPageShowsJoinLinkToHolder(t *testing.T) {
	h := newTicketHost(t) // gigEntity: hybrid, join_link_public "false"
	r := getTicket(t, h.token(t, kindTicket))
	if !strings.Contains(r.Body, `href="https://meet.example.org/x"`) {
		t.Error("the holder sees the join link even when it is not public")
	}
	h.entity = strings.Replace(gigEntity, `"hybrid"`, `"offline"`, 1)
	if r := getTicket(t, h.token(t, kindTicket)); strings.Contains(r.Body, "meet.example.org") {
		t.Error("an offline event has no join link")
	}
}

func TestTicketPageSurvivesHostError(t *testing.T) {
	h := newTicketHost(t)
	h.entityErr = errors.New("host exploded")
	r := getTicket(t, h.token(t, kindTicket))
	if r.Status != 200 || !strings.Contains(r.Body, "Gig (stored)") {
		t.Fatalf("%d %s", r.Status, r.Body)
	}
	if strings.Contains(r.Body, "exploded") {
		t.Error("host error text in the page")
	}
}

func TestTicketPageCancelled(t *testing.T) {
	h := newTicketHost(t)
	h.ticket = strings.Replace(holderRow, `"active"`, `"cancelled"`, 1)
	r := getTicket(t, h.token(t, kindTicket))
	if !strings.Contains(r.Body, "Cancelled") || strings.Contains(r.Body, "data:image") || strings.Contains(r.Body, "meet.example.org") {
		t.Errorf("a cancelled ticket shows no QR and no join link: %s", r.Body)
	}
}

func TestTicketNotFoundIsUniform(t *testing.T) {
	h := newTicketHost(t)
	good := h.token(t, kindTicket)
	var bodies []string
	for name, tok := range map[string]string{
		"forged":  flipTok(good),
		"kind":    h.token(t, kindWait),
		"garbage": "nope",
	} {
		r := getTicket(t, tok)
		if r.Status != 404 {
			t.Errorf("%s: status %d", name, r.Status)
		}
		bodies = append(bodies, r.Body)
	}
	h.ticket = "null" // unknown code, or an anonymised order
	r := getTicket(t, good)
	bodies = append(bodies, r.Body)
	if r.Status != 404 {
		t.Errorf("unknown code: %d", r.Status)
	}
	for _, b := range bodies {
		if b != bodies[0] {
			t.Errorf("answers differ: %q vs %q", b, bodies[0])
		}
	}
	if len(h.lookups) != 1 {
		t.Errorf("a forged token must not reach the database: %v", h.lookups)
	}
}

func TestTicketQRRoute(t *testing.T) {
	h := newTicketHost(t)
	tok := h.token(t, kindTicket)
	r := publicGet(t, "/ticket/{token}/qr.png", map[string]string{"token": tok}, nil)
	if r.Status != 200 || r.Headers["Content-Type"] != "image/png" || r.Headers["Cache-Control"] != "public, max-age=86400, immutable" {
		t.Fatalf("%d %v", r.Status, r.Headers)
	}
	raw, err := base64.StdEncoding.DecodeString(r.BodyB64)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := png.Decode(bytes.NewReader(raw)); err != nil {
		t.Error(err)
	}
	want, _ := qrPNG(ticketURL("https://example.org", tok), qrSize)
	if !bytes.Equal(raw, want) {
		t.Error("the QR holds the absolute ticket URL when the origin is known")
	}
	if r := publicGet(t, "/ticket/{token}/qr.png", map[string]string{"token": flipTok(tok)}, nil); r.Status != 404 || r.BodyB64 != "" {
		t.Errorf("forged: %d", r.Status)
	}
	h.ticket = "null"
	if r := publicGet(t, "/ticket/{token}/qr.png", map[string]string{"token": tok}, nil); r.Status != 404 {
		t.Errorf("unknown: %d", r.Status)
	}
}
