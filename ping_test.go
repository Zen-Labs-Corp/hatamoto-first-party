package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const goodID = "6f1c0a9e5b7d4e28a3c1f09d7b2e4a65"

func body(id, version, channel string) string {
	return `{"install":"` + id + `","version":"` + version + `","channel":"` + channel + `"}`
}

func send(t *testing.T, req sdk.HTTPRequest) sdk.HTTPResponse {
	t.Helper()
	if req.Method == "" {
		req.Method, req.Pattern = "POST", "/v1/ping"
	}
	in, _ := json.Marshal(req)
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

func jsonReq(b string) sdk.HTTPRequest {
	return sdk.HTTPRequest{Body: b, Headers: map[string]string{"content-type": "application/json"}}
}

func TestValidPing(t *testing.T) {
	calls := recordHost(t)
	res := send(t, jsonReq(body(goodID, "v1.2.3", "stable")))
	if res.Status != 204 || res.Body != "" {
		t.Fatalf("got %d %q", res.Status, res.Body)
	}
	if len(*calls) != 1 || (*calls)[0].fn != "db.exec" || !sqlGuardOK((*calls)[0].sql) {
		t.Fatalf("calls %+v", *calls)
	}
}

func TestSameDayPingsShareTheKey(t *testing.T) {
	defer func(f func() time.Time) { now = f }(now)
	now = func() time.Time { return time.Date(2026, 10, 28, 12, 0, 0, 0, time.UTC) }
	calls := recordHost(t)
	send(t, jsonReq(body(goodID, "v1.2.3", "stable")))
	send(t, jsonReq(body(goodID, "v1.2.4", "edge")))
	if len(*calls) != 2 {
		t.Fatalf("%d calls", len(*calls))
	}
	a, b := (*calls)[0].args, (*calls)[1].args
	if a[0] != b[0] || a[1] != b[1] || a[1] != "2026-10-28" {
		t.Fatalf("keys differ: %v %v", a, b)
	}
	if b[2] != "v1.2.4" || b[3] != "edge" {
		t.Fatalf("latest values missing: %v", b)
	}
}

func TestRejectedPings(t *testing.T) {
	big := `{"install":"` + goodID + `","version":"v1.2.3","channel":"stable","x":"` + strings.Repeat("a", 1024) + `"}`
	longVersion := "v1.2.3-" + strings.Repeat("a", 60)
	cases := []struct {
		name string
		req  sdk.HTTPRequest
		want int
	}{
		{"extra key", jsonReq(`{"install":"` + goodID + `","version":"v1.2.3","channel":"stable","x":"y"}`), 400},
		{"missing key", jsonReq(`{"install":"` + goodID + `","version":"v1.2.3"}`), 400},
		{"duplicate key", jsonReq(`{"install":"` + goodID + `","install":"` + goodID + `","version":"v1.2.3","channel":"stable"}`), 400},
		{"case-variant key", jsonReq(`{"Install":"` + goodID + `","version":"v1.2.3","channel":"stable"}`), 400},
		{"number value", jsonReq(`{"install":"` + goodID + `","version":1,"channel":"stable"}`), 400},
		{"null value", jsonReq(`{"install":"` + goodID + `","version":null,"channel":"stable"}`), 400},
		{"null body", jsonReq(`null`), 400},
		{"array body", jsonReq(`[]`), 400},
		{"not json", jsonReq(`install=1`), 400},
		{"trailing object", jsonReq(body(goodID, "v1.2.3", "stable") + `{}`), 400},
		{"trailing garbage", jsonReq(body(goodID, "v1.2.3", "stable") + ` x`), 400},
		{"truncated", jsonReq(`{"install":"` + goodID + `"`), 400},
		{"empty body", jsonReq(``), 400},
		{"body_b64", sdk.HTTPRequest{BodyB64: "e30=", Headers: map[string]string{"content-type": "application/json"}}, 400},
		{"over cap", jsonReq(big), 413},
		{"no content type", sdk.HTTPRequest{Body: body(goodID, "v1.2.3", "stable")}, 415},
		{"form content type", sdk.HTTPRequest{Body: body(goodID, "v1.2.3", "stable"), Headers: map[string]string{"content-type": "text/plain"}}, 415},
		{"uppercase hex", jsonReq(body(strings.ToUpper(goodID), "v1.2.3", "stable")), 422},
		{"31 chars", jsonReq(body(goodID[:31], "v1.2.3", "stable")), 422},
		{"33 chars", jsonReq(body(goodID+"a", "v1.2.3", "stable")), 422},
		{"build metadata", jsonReq(body(goodID, "v1.2.3+meta", "stable")), 422},
		{"no v", jsonReq(body(goodID, "1.2.3", "stable")), 422},
		{"latest", jsonReq(body(goodID, "latest", "stable")), 422},
		{"version over 64", jsonReq(body(goodID, longVersion+strings.Repeat("a", 10), "stable")), 422},
		{"channel beta", jsonReq(body(goodID, "v1.2.3", "beta")), 422},
		{"channel upper", jsonReq(body(goodID, "v1.2.3", "Stable")), 422},
	}
	for _, c := range cases {
		calls := recordHost(t)
		if res := send(t, c.req); res.Status != c.want || res.Body != "" {
			t.Errorf("%s: got %d %q, want %d", c.name, res.Status, res.Body, c.want)
		}
		if len(*calls) != 0 {
			t.Errorf("%s: %d db calls", c.name, len(*calls))
		}
	}
}

func TestAcceptedVersionsAndContentTypeParams(t *testing.T) {
	for _, v := range []string{"v1.2.3", "v1.8.2-rc.1", "v1.2.3-beta.1", "v1.2.3-edge.5", "v0.0.1-beta", "v10.20.30"} {
		calls := recordHost(t)
		if res := send(t, jsonReq(body(goodID, v, "canary"))); res.Status != 204 || len(*calls) != 1 {
			t.Errorf("%s: %d, %d calls", v, res.Status, len(*calls))
		}
	}
	req := sdk.HTTPRequest{Body: body(goodID, "v1.2.3", "edge"), Headers: map[string]string{"content-type": "Application/JSON; charset=utf-8"}}
	if res := send(t, req); res.Status != 204 {
		t.Errorf("content type with params: %d", res.Status)
	}
}

func TestDatabaseFailureIs503(t *testing.T) {
	for _, hostErr := range []error{errors.New("capability_denied: db"), errors.New("connection refused")} {
		callHost = func(_ string, _, _ any) error { return hostErr }
		res := send(t, jsonReq(body(goodID, "v1.2.3", "stable")))
		if res.Status != 503 || res.Body != "" {
			t.Errorf("%v: got %d %q", hostErr, res.Status, res.Body)
		}
	}
}

func TestHeadersNeverReachTheDatabase(t *testing.T) {
	calls := recordHost(t)
	req := jsonReq(body(goodID, "v1.2.3", "stable"))
	req.Headers["user-agent"] = "hatamoto-stats"
	req.Headers["referer"] = "https://example.invalid/secret"
	send(t, req)
	if len(*calls) != 1 {
		t.Fatal("no call")
	}
	c := (*calls)[0]
	all := c.sql + strings.Join(strings.Fields(strings.TrimSpace(strings.Join(anyStrings(c.args), " "))), " ")
	if strings.Contains(all, "hatamoto-stats") || strings.Contains(all, "secret") || len(c.args) != 4 {
		t.Fatalf("leak: %v", c.args)
	}
}

func anyStrings(in []any) []string {
	out := make([]string, len(in))
	for i, v := range in {
		out[i], _ = v.(string)
	}
	return out
}

func TestUnknownRouteIs404(t *testing.T) {
	if res := send(t, sdk.HTTPRequest{Method: "GET", Pattern: "/nope"}); res.Status != 404 {
		t.Fatalf("got %d", res.Status)
	}
}
