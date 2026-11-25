package main

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func seatCall(t *testing.T, req sdk.HTTPRequest) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(req)
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var resp sdk.HTTPResponse
	_ = json.Unmarshal(out, &resp)
	return resp
}

func bodyOf(resp sdk.HTTPResponse) map[string]any {
	var m map[string]any
	_ = json.Unmarshal([]byte(resp.Body), &m)
	return m
}

func (h *payHost) seatHoldPost(t *testing.T, fields map[string]any) sdk.HTTPResponse {
	t.Helper()
	body := map[string]any{"event": "e1", "section": 3, "row": 0, "num": 2, "token": h.token(t)}
	for k, v := range fields {
		body[k] = v
	}
	return seatCall(t, sdk.HTTPRequest{Method: "POST", Pattern: "/seats/hold", Body: jsonText(body),
		Headers: map[string]string{"content-type": "application/json"}})
}

func seatDelete(tok string, query map[string][]string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Method: "DELETE", Pattern: "/seats/hold/{token}", PathValues: map[string]string{"token": tok}, Query: query}
}

func TestSeatMapIsSharedBriefly(t *testing.T) {
	h := seatedHost(t)
	resp := seatCall(t, sdk.HTTPRequest{Method: "GET", Pattern: "/seats/{event}", PathValues: map[string]string{"event": "e1"}})
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "public, max-age=5" {
		t.Fatalf("%d %v", resp.Status, resp.Headers)
	}
	m := bodyOf(resp)
	secs, _ := m["sections"].([]any)
	if m["ok"] != true || len(secs) != 2 || !strings.Contains(resp.Body, `"s":"xooo"`) || !strings.Contains(resp.Body, `"price":"`) {
		t.Errorf("body %s", resp.Body)
	}
	for _, c := range h.calls {
		if strings.Contains(c.sql, "attendees a") && strings.Contains(c.sql, "email") {
			t.Error("availability read buyer data")
		}
	}
	h.avail = `[]`
	if resp = seatCall(t, sdk.HTTPRequest{Method: "GET", Pattern: "/seats/{event}", PathValues: map[string]string{"event": "e1"}}); resp.Status != 404 || resp.Headers["Cache-Control"] == seatMapMaxAge {
		t.Errorf("no seated type: %d %v", resp.Status, resp.Headers)
	}
	h.avail = seatedAvail
	h.clock = h.clock.Add(10 * 365 * 24 * time.Hour) // the event is over
	if resp = seatCall(t, sdk.HTTPRequest{Method: "GET", Pattern: "/seats/{event}", PathValues: map[string]string{"event": "e1"}}); resp.Status != 404 {
		t.Errorf("an event that is not open serves no map: %d", resp.Status)
	}
	if resp = seatCall(t, sdk.HTTPRequest{Method: "GET", Pattern: "/seats/{event}", PathValues: map[string]string{"event": "a b"}}); resp.Status != 404 {
		t.Errorf("bad event id: %d", resp.Status)
	}
}

func TestSeatHoldFirstSeatMakesACode(t *testing.T) {
	h := seatedHost(t)
	h.seatHold = []string{`{"status":"held","seat_id":41,"label":"Stalls A-2","expires_in":600}`}
	resp := h.seatHoldPost(t, nil)
	m := bodyOf(resp)
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "no-store" || m["ok"] != true || m["seat_id"] != float64(41) ||
		m["label"] != "Stalls A-2" || m["expires_in"] != float64(600) {
		t.Fatalf("%d %v %s", resp.Status, resp.Headers, resp.Body)
	}
	claims := h.with("INSERT INTO seat_holds")
	if len(claims) != 1 {
		t.Fatalf("%d claims", len(claims))
	}
	code, _ := claims[0].args[1].(string)
	tok, _ := m["hold"].(string)
	if got, ok := verifyToken(h.key, kindHold, tok); !ok || got != code || len(code) != 32 {
		t.Errorf("token %q is not the signed hold code %q", tok, code)
	}
	if !same(claims[0].args[0], "e1") || !same(claims[0].args[2], 3) || !same(claims[0].args[3], 0) || !same(claims[0].args[4], 2) {
		t.Errorf("args %v", claims[0].args)
	}

	// the next pick carries the token and joins the same code
	h.calls = nil
	if resp = h.seatHoldPost(t, map[string]any{"hold": tok, "num": 3}); resp.Status != 200 {
		t.Fatalf("%d %s", resp.Status, resp.Body)
	}
	if again := h.with("INSERT INTO seat_holds"); len(again) != 1 || again[0].args[1] != code {
		t.Errorf("second seat: %+v", again)
	}
}

func TestSeatHoldAnswers(t *testing.T) {
	h := seatedHost(t)
	h.seatHold = []string{`{"status":"taken"}`}
	if resp := h.seatHoldPost(t, nil); resp.Status != 409 || resp.Body != `{"error":"taken","ok":false}` || resp.Headers["Cache-Control"] != "no-store" {
		t.Errorf("taken: %d %s", resp.Status, resp.Body)
	}
	h.seatHold = []string{`{"status":"full"}`}
	if resp := h.seatHoldPost(t, nil); resp.Status != 429 || !strings.Contains(resp.Body, `"full"`) {
		t.Errorf("full: %d %s", resp.Status, resp.Body)
	}
	h.seatHold = []string{`{"status":"gone"}`}
	if resp := h.seatHoldPost(t, nil); resp.Status != 409 {
		t.Errorf("gone: %d %s", resp.Status, resp.Body)
	}
}

func TestSeatHoldRefusesBadPosts(t *testing.T) {
	h := seatedHost(t)
	post := func(ct, body string) int {
		return seatCall(t, sdk.HTTPRequest{Method: "POST", Pattern: "/seats/hold", Body: body, Headers: map[string]string{"content-type": ct}}).Status
	}
	good := `{"event":"e1","section":3,"row":0,"num":2,"token":"` + h.token(t) + `"}`
	if got := post("text/plain", good); got != 415 {
		t.Errorf("content type: %d", got)
	}
	if got := post("application/json", `{"event":"e1","pad":"`+strings.Repeat("x", 5000)+`"}`); got != 413 {
		t.Errorf("size: %d", got)
	}
	if got := post("application/json", `{"event":"e1","section":3,"colour":"red"}`); got != 400 {
		t.Errorf("unknown key: %d", got)
	}
	if got := post("application/json", `{"event":"e1",`); got != 400 {
		t.Errorf("not json: %d", got)
	}
	for name, f := range map[string]map[string]any{
		"event": {"event": "a b"}, "section": {"section": 0}, "row": {"row": -1}, "row high": {"row": 60},
		"num": {"num": 0}, "num high": {"num": 101}, "hold token": {"hold": "nope"},
	} {
		if resp := h.seatHoldPost(t, f); resp.Status != 422 || resp.Headers["Cache-Control"] != "no-store" {
			t.Errorf("%s: %d %s", name, resp.Status, resp.Body)
		}
	}
	if len(h.with("INSERT INTO seat_holds")) != 0 {
		t.Error("a refused post held a seat")
	}
}

func TestSeatHoldBotsGetOKAndHoldNothing(t *testing.T) {
	h := seatedHost(t)
	stale := h.clock.Add(-2 * time.Hour).UnixMilli()
	issued := jsonText(stale)
	cases := map[string]map[string]any{
		"honeypot":    {"website": "http://x.example"},
		"stale token": {"token": strings.Trim(issued, `"`) + "." + tokenMAC(h.key, strings.Trim(issued, `"`))},
		"no token":    {"token": ""},
		"forged":      {"token": "1.ab"},
	}
	for name, f := range cases {
		if resp := h.seatHoldPost(t, f); resp.Status != 200 || strings.Contains(resp.Body, "hold") {
			t.Errorf("%s: %d %s", name, resp.Status, resp.Body)
		}
	}
	if len(h.with("INSERT INTO seat_holds")) != 0 {
		t.Error("a bot held a seat")
	}
}

func TestSeatRelease(t *testing.T) {
	h := seatedHost(t)
	tok := holdToken(h, holdCodeA)
	q := func(event, seat string) map[string][]string {
		out := map[string][]string{"event": {event}}
		if seat != "" {
			out["seat"] = []string{seat}
		}
		return out
	}
	resp := seatCall(t, seatDelete(tok, q("e1", "41")))
	if resp.Status != 200 || resp.Headers["Cache-Control"] != "no-store" || !strings.Contains(resp.Body, `"released":1`) {
		t.Fatalf("%d %v %s", resp.Status, resp.Headers, resp.Body)
	}
	rel := h.releases()
	if len(rel) != 1 || !same(rel[0].args[0], "e1") || rel[0].args[1] != holdCodeA || !same(rel[0].args[2], 41) {
		t.Errorf("release %+v", rel)
	}
	h.calls = nil
	if resp = seatCall(t, seatDelete(tok, q("e1", ""))); resp.Status != 200 {
		t.Fatalf("%d", resp.Status)
	}
	if rel = h.releases(); len(rel) != 1 || !same(rel[0].args[2], 0) {
		t.Errorf("all seats: %+v", rel)
	}
	h.calls = nil
	for name, req := range map[string]sdk.HTTPRequest{
		"bad token":   seatDelete("nope", q("e1", "41")),
		"other kind":  seatDelete(signCode(h.key, kindTicket, holdCodeA), q("e1", "41")),
		"forged code": seatDelete("00000000000000000000000000000000."+strings.Repeat("0", 32), q("e1", "41")),
	} {
		if resp := seatCall(t, req); resp.Status != 404 {
			t.Errorf("%s: %d", name, resp.Status)
		}
	}
	for name, req := range map[string]sdk.HTTPRequest{
		"bad event": seatDelete(tok, q("a b", "41")), "bad seat": seatDelete(tok, q("e1", "x")), "zero seat": seatDelete(tok, q("e1", "0")),
		"no event": seatDelete(tok, nil),
	} {
		if resp := seatCall(t, req); resp.Status != 422 {
			t.Errorf("%s: %d", name, resp.Status)
		}
	}
	if len(h.releases()) != 0 {
		t.Error("a refused release deleted seats")
	}
}

func TestSeatRoutesAreMethodBound(t *testing.T) {
	seatedHost(t)
	for _, req := range []sdk.HTTPRequest{
		{Method: "GET", Pattern: "/seats/hold"}, {Method: "POST", Pattern: "/seats/{event}"},
		{Method: "GET", Pattern: "/seats/hold/{token}"}, {Method: "DELETE", Pattern: "/seats/{event}"},
	} {
		if resp := seatCall(t, req); resp.Status != 404 {
			t.Errorf("%s %s: %d", req.Method, req.Pattern, resp.Status)
		}
	}
}
