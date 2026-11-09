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

const detailedBody = `{"install":"` + goodID + `","version":"v1.1.0","channel":"stable","level":"detailed","os":"linux","arch":"amd64","postgres":16,"age_months":6,"worker":true,"theme":{"name":"hatamoto-dev","version":"1.6.0"},"plugins":[{"name":"gtm","version":"1.0.0","enabled":false},{"name":"searchconsole","version":"1.0.0","enabled":true}],"other_plugins":2,"health":{"since_hours":24,"panics":0,"http_5xx":3,"render_errors":0,"plugin_errors":{"searchconsole":1,"other":4},"update_failed":0,"update_rolled_back":0,"migration_failed":0}}`

// mutate replaces one fragment of the valid Detailed body.
func mutate(t *testing.T, from, to string) string {
	t.Helper()
	if !strings.Contains(detailedBody, from) {
		t.Fatalf("fragment %q not in body", from)
	}
	return strings.Replace(detailedBody, from, to, 1)
}

func TestValidDetailedPing(t *testing.T) {
	p, code := parseDetailed(detailedBody)
	if code != 0 || p.Postgres != 16 || len(p.Plugins) != 2 || p.Plugins[1].Errors != 1 || p.Health.OtherErrors != 4 || p.Health.HTTP5xx != 3 || p.ThemeName != "hatamoto-dev" {
		t.Fatalf("code %d, %+v", code, p)
	}
	if res := send(t, jsonReq(detailedBody)); res.Status != 204 {
		t.Fatalf("got %d", res.Status)
	}
	empty := mutate(t, `[{"name":"gtm","version":"1.0.0","enabled":false},{"name":"searchconsole","version":"1.0.0","enabled":true}]`, `[]`)
	empty = strings.Replace(empty, `{"searchconsole":1,"other":4}`, `{}`, 1)
	if _, code := parseDetailed(empty); code != 0 {
		t.Fatalf("empty plugins: %d", code)
	}
}

func TestRejectedDetailedPings(t *testing.T) {
	p101 := func() string {
		var ps []string
		for i := 0; i < 101; i++ {
			ps = append(ps, `{"name":"plug`+string(rune('a'+i/26))+string(rune('a'+i%26))+`","version":"1.0.0","enabled":true}`)
		}
		return "[" + strings.Join(ps, ",") + "]"
	}()
	plugins := `[{"name":"gtm","version":"1.0.0","enabled":false},{"name":"searchconsole","version":"1.0.0","enabled":true}]`
	errs := `{"searchconsole":1,"other":4}`
	cases := []struct {
		name, body string
		want       int
	}{
		{"level basic", mutate(t, `"level":"detailed"`, `"level":"basic"`), 422},
		{"level case", mutate(t, `"detailed"`, `"Detailed"`), 422},
		{"level number", mutate(t, `"level":"detailed"`, `"level":1`), 422},
		{"basic body with level", `{"install":"` + goodID + `","version":"v1.1.0","channel":"stable","level":"basic"}`, 422},
		{"missing key", mutate(t, `"os":"linux",`, ``), 422},
		{"extra key", mutate(t, `"worker":true`, `"worker":true,"ip":"x"`), 422},
		{"case-variant key", mutate(t, `"os":`, `"OS":`), 422},
		{"duplicate top key", mutate(t, `"worker":true`, `"worker":true,"worker":true`), 422},
		{"duplicate theme key", mutate(t, `"name":"hatamoto-dev"`, `"name":"hatamoto-dev","name":"x"`), 422},
		{"duplicate plugin key", mutate(t, `"enabled":false`, `"enabled":false,"enabled":false`), 422},
		{"duplicate health key", mutate(t, `"panics":0`, `"panics":0,"panics":0`), 422},
		{"duplicate plugin_errors key", mutate(t, `"other":4`, `"other":4,"other":4`), 422},
		{"extra theme key", mutate(t, `"version":"1.6.0"`, `"version":"1.6.0","x":1`), 422},
		{"extra plugin key", mutate(t, `"enabled":false`, `"enabled":false,"x":1`), 422},
		{"extra health key", mutate(t, `"panics":0`, `"panics":0,"x":1`), 422},
		{"null value", mutate(t, `"os":"linux"`, `"os":null`), 422},
		{"null theme", mutate(t, `{"name":"hatamoto-dev","version":"1.6.0"}`, `null`), 422},
		{"null plugins", mutate(t, plugins, `null`), 422},
		{"postgres string", mutate(t, `"postgres":16`, `"postgres":"16"`), 422},
		{"postgres fraction", mutate(t, `"postgres":16`, `"postgres":16.0`), 422},
		{"postgres 8", mutate(t, `"postgres":16`, `"postgres":8`), 422},
		{"postgres 100", mutate(t, `"postgres":16`, `"postgres":100`), 422},
		{"worker string", mutate(t, `"worker":true`, `"worker":"true"`), 422},
		{"age 5", mutate(t, `"age_months":6`, `"age_months":5`), 422},
		{"age 37", mutate(t, `"age_months":6`, `"age_months":37`), 422},
		{"age negative", mutate(t, `"age_months":6`, `"age_months":-1`), 422},
		{"os upper", mutate(t, `"linux"`, `"Linux"`), 422},
		{"os empty", mutate(t, `"linux"`, `""`), 422},
		{"os 17 chars", mutate(t, `"linux"`, `"`+strings.Repeat("a", 17)+`"`), 422},
		{"arch dash", mutate(t, `"amd64"`, `"amd-64"`), 422},
		{"channel beta", mutate(t, `"stable"`, `"beta"`), 422},
		{"core version no v", mutate(t, `"v1.1.0"`, `"1.1.0"`), 422},
		{"bad install", mutate(t, goodID, goodID[:31]), 422},
		{"theme name upper", mutate(t, `"hatamoto-dev"`, `"Hatamoto"`), 422},
		{"theme name dash first", mutate(t, `"hatamoto-dev"`, `"-x"`), 422},
		{"theme name 65", mutate(t, `"hatamoto-dev"`, `"`+strings.Repeat("a", 65)+`"`), 422},
		{"theme version v prefix", mutate(t, `"1.6.0"`, `"v1.6.0"`), 422},
		{"theme version build", mutate(t, `"1.6.0"`, `"1.6.0+b"`), 422},
		{"theme version 65", mutate(t, `"1.6.0"`, `"1.0.0-`+strings.Repeat("a", 59)+`"`), 422},
		{"plugin version build", mutate(t, `"version":"1.0.0","enabled":false`, `"version":"1.0.0+x","enabled":false`), 422},
		{"plugin name upper", mutate(t, `"name":"gtm"`, `"name":"GTM"`), 422},
		{"plugins unsorted", mutate(t, plugins, `[{"name":"searchconsole","version":"1.0.0","enabled":true},{"name":"gtm","version":"1.0.0","enabled":false}]`), 422},
		{"plugins duplicate", mutate(t, plugins, `[{"name":"gtm","version":"1.0.0","enabled":true},{"name":"gtm","version":"1.0.0","enabled":true}]`), 422},
		{"plugins 101", mutate(t, plugins, p101), 422},
		{"plugin enabled string", mutate(t, `"enabled":false`, `"enabled":"no"`), 422},
		{"other_plugins negative", mutate(t, `"other_plugins":2`, `"other_plugins":-1`), 422},
		{"other_plugins 1001", mutate(t, `"other_plugins":2`, `"other_plugins":1001`), 422},
		{"since_hours 10001", mutate(t, `"since_hours":24`, `"since_hours":10001`), 422},
		{"panics negative", mutate(t, `"panics":0`, `"panics":-1`), 422},
		{"http_5xx over", mutate(t, `"http_5xx":3`, `"http_5xx":1000001`), 422},
		{"render_errors string", mutate(t, `"render_errors":0`, `"render_errors":"0"`), 422},
		{"update_failed over", mutate(t, `"update_failed":0`, `"update_failed":1000001`), 422},
		{"update_rolled_back negative", mutate(t, `"update_rolled_back":0`, `"update_rolled_back":-5`), 422},
		{"migration_failed over", mutate(t, `"migration_failed":0`, `"migration_failed":2000000`), 422},
		{"plugin_errors unlisted", mutate(t, `"searchconsole":1,`, `"gtmx":1,`), 422},
		{"plugin_errors bad name", mutate(t, `"searchconsole":1,`, `"Bad":1,`), 422},
		{"plugin_errors over", mutate(t, `"searchconsole":1`, `"searchconsole":1000001`), 422},
		{"plugin_errors string", mutate(t, `"searchconsole":1`, `"searchconsole":"1"`), 422},
		{"plugin_errors array", mutate(t, errs, `[]`), 422},
		{"not json", `{"level":"detailed"`, 400},
		{"trailing data", detailedBody + ` {}`, 400},
		{"trailing garbage", detailedBody + ` x`, 400},
		{"over 16384", mutate(t, `"os":"linux"`, `"os":"`+strings.Repeat("a", 17000)+`"`), 413},
	}
	for _, c := range cases {
		calls := recordHost(t)
		if res := send(t, jsonReq(c.body)); res.Status != c.want || res.Body != "" {
			t.Errorf("%s: got %d %q, want %d", c.name, res.Status, res.Body, c.want)
		}
		if len(*calls) != 0 {
			t.Errorf("%s: %d host calls", c.name, len(*calls))
		}
	}
}

func TestDetailedLimits(t *testing.T) {
	var ps, es []string
	for i := 0; i < 101; i++ {
		name := "plug" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		ps = append(ps, `{"name":"`+name+`","version":"1.0.0","enabled":true}`)
		es = append(es, `"`+name+`":1`)
	}
	withN := func(n int) string {
		b := mutate(t, `[{"name":"gtm","version":"1.0.0","enabled":false},{"name":"searchconsole","version":"1.0.0","enabled":true}]`, "["+strings.Join(ps[:n], ",")+"]")
		return strings.Replace(b, `{"searchconsole":1,"other":4}`, "{"+strings.Join(es[:n], ",")+"}", 1)
	}
	if _, code := parseDetailed(withN(100)); code != 0 {
		t.Errorf("100 plugins: %d", code)
	}
	if _, code := parseDetailed(withN(101)); code != 422 {
		t.Errorf("101 plugins: %d", code)
	}
	// plugin_errors with 101 keys while every key is listed is still too many.
	b := strings.Replace(withN(100), `"plugaa":1`, `"plugaa":1,"other":1`, 1)
	if _, code := parseDetailed(b); code != 422 {
		t.Errorf("101 plugin_errors keys: %d", code)
	}

	// Sizes: Basic above 1 KiB is 413; Detailed between 1 KiB and 16 KiB is accepted.
	pad := func(n int) string { return mutate(t, `"os":"linux"`, `"os":"linux"`) + strings.Repeat(" ", n) }
	if len(detailedBody) >= 1024 {
		t.Fatal("fixture should be small")
	}
	big := withN(100)
	if len(big) <= 1024 || len(big) > 16384 {
		t.Fatalf("fixture size %d", len(big))
	}
	if res := send(t, jsonReq(big)); res.Status != 204 {
		t.Errorf("1 KiB < Detailed <= 16384: %d", res.Status)
	}
	exact := pad(16384 - len(detailedBody))
	if len(exact) != 16384 {
		t.Fatalf("len %d", len(exact))
	}
	if res := send(t, jsonReq(exact)); res.Status != 204 {
		t.Errorf("exactly 16384: %d", res.Status)
	}
	if res := send(t, jsonReq(exact+" ")); res.Status != 413 {
		t.Errorf("16385: %d", res.Status)
	}
	basic := body(goodID, "v1.2.3", "stable")
	if res := send(t, jsonReq(basic+strings.Repeat(" ", 1024-len(basic)))); res.Status != 204 {
		t.Errorf("Basic exactly 1024: %d", res.Status)
	}
	if res := send(t, jsonReq(basic+strings.Repeat(" ", 1025-len(basic)))); res.Status != 413 {
		t.Errorf("Basic 1025: %d", res.Status)
	}
}
