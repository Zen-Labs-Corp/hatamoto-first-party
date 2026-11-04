package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const cannedRows = `[{"submission_id":"a1","form_slug":"contact","status":"sent","reason":"","meta_code":200,"meta_message":null,"test":false,"created_at":"2026-11-04T20:31:00Z"}]`

// routeHost wraps the delivery fake with the two host calls only the routes use.
func routeHost(t *testing.T, rows string, granted []string) *host {
	h := newHost(t)
	base := h.call
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "db.query":
			b, _ := json.Marshal(args)
			h.calls = append(h.calls, call{fn, string(b), args})
			cell, _ := json.Marshal(rows)
			return json.Unmarshal([]byte(`{"rows":[[`+string(cell)+`]]}`), out)
		case "permissions":
			b, _ := json.Marshal(args)
			h.calls = append(h.calls, call{fn, string(b), args})
			res, _ := json.Marshal(map[string]any{"granted": granted})
			return json.Unmarshal(res, out)
		}
		return base(fn, args, out)
	}
	return h
}

func request(t *testing.T, method, pattern string) (int, map[string]any, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Path: pattern, Pattern: pattern})
	if err != nil {
		t.Fatal(err)
	}
	var body map[string]any
	if err := json.Unmarshal([]byte(res.Body), &body); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return res.Status, body, res.Body
}

func TestGetLogReturnsRowsAndSummary(t *testing.T) {
	h := routeHost(t, cannedRows, []string{"forms.read", "db"})
	h.configured(`"forms":["contact"]`, `"test_event_code":"TEST1"`)
	status, body, raw := request(t, "GET", "/log")
	if status != 200 || body["configured"] != true || body["pixel_id"] != "123456789" ||
		body["graph_version"] != "v26.0" || body["test_event_code"] != "TEST1" || body["forms_read_granted"] != true {
		t.Fatalf("%d %s", status, raw)
	}
	if rows := body["rows"].([]any); len(rows) != 1 || rows[0].(map[string]any)["submission_id"] != "a1" {
		t.Fatalf("rows = %v", body["rows"])
	}
	if strings.Contains(raw, sampleToken) {
		t.Fatal("token in the response")
	}
	q := h.callsOf("db.query")[0].args
	if !strings.Contains(q, "LIMIT 200") || !strings.Contains(q, "ORDER BY created_at DESC") {
		t.Fatalf("query = %s", q)
	}
	for _, bad := range []string{"public", "pg_"} {
		if strings.Contains(q, bad) {
			t.Fatalf("query contains %q", bad)
		}
	}
}

func TestGetLogNotConfiguredAndEmpty(t *testing.T) {
	routeHost(t, `[]`, nil)
	status, body, _ := request(t, "GET", "/log")
	if status != 200 || body["configured"] != false || body["forms_read_granted"] != false {
		t.Fatalf("%d %v", status, body)
	}
	if m := body["missing"].([]any); len(m) != 2 || m[0] != "pixel_id" || m[1] != "access_token" {
		t.Fatalf("missing = %v", m)
	}
	if rows, ok := body["rows"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("rows = %v", body["rows"])
	}
	if f, ok := body["forms"].([]any); !ok || len(f) != 0 {
		t.Fatalf("forms = %v", body["forms"])
	}
}

func TestPostTestNeedsSettings(t *testing.T) {
	for name, set := range map[string]string{
		"no test code": `{"pixel_id":"123456789","access_token":"` + sampleToken + `"}`,
		"no pixel":     `{"access_token":"` + sampleToken + `","test_event_code":"T1"}`,
		"no token":     `{"pixel_id":"123456789","test_event_code":"T1"}`,
	} {
		h := routeHost(t, `[]`, nil)
		h.settings = set
		status, body, raw := request(t, "POST", "/test")
		if status != 400 || body["error"] == "" || body["error"] == nil {
			t.Errorf("%s: %d %s", name, status, raw)
		}
		if h.count("http.fetch") != 0 || len(h.rows) != 0 {
			t.Errorf("%s: sent or logged something", name)
		}
	}
}

func TestPostTestNamesMissingSettings(t *testing.T) {
	h := routeHost(t, `[]`, nil)
	h.settings = `{"pixel_id":"123456789","access_token":"` + sampleToken + `","graph_version":"bogus","test_event_code":"T1"}`
	status, body, raw := request(t, "POST", "/test")
	msg, _ := body["error"].(string)
	if status != 400 || !strings.Contains(msg, "Graph API version") || strings.Contains(msg, "Pixel ID") {
		t.Fatalf("%d %s", status, raw)
	}
	status, body, _ = request(t, "GET", "/log")
	if m := body["missing"].([]any); status != 200 || len(m) != 1 || m[0] != "graph_version" || body["event_name"] != "Lead" {
		t.Fatalf("%d %v", status, body)
	}
}

func TestPostTestSendsAndLogsATestRow(t *testing.T) {
	h := routeHost(t, `[]`, nil)
	h.configured(`"test_event_code":"TEST1"`)
	status, body, raw := request(t, "POST", "/test")
	if status != 200 || body["ok"] != true || body["code"] != float64(200) {
		t.Fatalf("%d %s", status, raw)
	}
	_, ev := h.sent(t)
	if ev["test_event_code"] != "TEST1" {
		t.Fatalf("event = %v", ev)
	}
	d := ev["data"].([]any)[0].(map[string]any)
	id, _ := d["event_id"].(string)
	if !strings.HasPrefix(id, "test-") {
		t.Fatalf("event_id = %q", id)
	}
	if d["user_data"].(map[string]any)["em"].([]any)[0] != sha("test@example.com") {
		t.Fatalf("user_data = %v", d["user_data"])
	}
	r := h.rows[id]
	if r == nil || !r.test || r.form != "test" || r.status != "sent" {
		t.Fatalf("row = %+v", r)
	}
	for _, s := range []string{sha("test@example.com"), "test@example.com", sampleToken} {
		for _, c := range h.calls {
			if c.fn != "http.fetch" && strings.Contains(c.args, s) {
				t.Errorf("%s call carries a secret or hash", c.fn)
			}
		}
		if strings.Contains(raw, s) {
			t.Errorf("response carries %q", s)
		}
	}
}

func TestPostTestMetaErrorIsReported(t *testing.T) {
	h := routeHost(t, `[]`, nil)
	h.configured(`"test_event_code":"TEST1"`)
	h.fetchStatus = 400
	h.fetchBody = `{"error":{"code":100,"message":"(#100) Invalid parameter ` + strings.Repeat("x", 400) + `"}}`
	status, body, raw := request(t, "POST", "/test")
	msg, _ := body["error"].(string)
	if status != 502 || !strings.HasPrefix(msg, "(#100) Invalid parameter") || len([]rune(msg)) > maxMessage {
		t.Fatalf("%d %s", status, raw)
	}
	if strings.Contains(raw, sampleToken) {
		t.Fatal("token in the response")
	}
	for _, r := range h.rows {
		if r.status != "failed" || !r.test {
			t.Fatalf("row = %+v", r)
		}
	}
}

func TestPostTestFetchErrorIs502(t *testing.T) {
	h := routeHost(t, `[]`, nil)
	h.configured(`"test_event_code":"TEST1"`)
	h.fetchErr = errHostDown
	status, body, _ := request(t, "POST", "/test")
	if status != 502 || body["error"] == "" {
		t.Fatalf("%d %v", status, body)
	}
	if h.count("log") != 1 {
		t.Fatalf("log lines = %d", h.count("log"))
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	routeHost(t, `[]`, nil)
	if status, _, _ := request(t, "GET", "/nope"); status != 404 {
		t.Fatalf("status = %d", status)
	}
}

func TestFetchHostErrorCodeIsStored(t *testing.T) {
	h := routeHost(t, `[]`, nil)
	h.configured(`"test_event_code":"TEST1"`)
	h.fetchErr = &sdk.HostError{Code: sdk.ErrCapabilityDenied, Capability: "http.fetch", Message: "secret detail " + sampleToken}
	status, body, raw := request(t, "POST", "/test")
	if status != 502 || body["error"] != "host: capability_denied http.fetch" {
		t.Fatalf("%d %s", status, raw)
	}
	for _, r := range h.rows {
		if r.reason != "http_error" || r.message != "host: capability_denied http.fetch" {
			t.Fatalf("row = %+v", r)
		}
	}
	if strings.Contains(raw, sampleToken) {
		t.Fatal("token in the response")
	}
}
