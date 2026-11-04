package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// routeHost answers settings, the log query and http.fetch.
func routeHost(t *testing.T, settings string, fetchStatus int, fetchBody string, fetchErr error) {
	t.Helper()
	(&fakeHost{settings: settings}).install(t)
	callHost = func(fn string, a, out any) error {
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(settings), out)
		case "db.query":
			return json.Unmarshal([]byte(`{"rows":[[[{"created_at":"2026-11-04T10:00:00Z","form_slug":"contact","status":"sent","reason":"","code":200}]]]}`), out)
		case "http.fetch":
			if fetchErr != nil {
				return fetchErr
			}
			b, _ := json.Marshal(map[string]any{"status": fetchStatus, "body": fetchBody})
			return json.Unmarshal(b, out)
		}
		return nil
	}
}

func call(t *testing.T, method, pattern string) (int, map[string]any, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern})
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Body), &m); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return res.Status, m, res.Body
}

func TestLogRouteShape(t *testing.T) {
	routeHost(t, baseSettings, 0, "", nil)
	code, m, body := call(t, "GET", "/log")
	if code != 200 || m["provider"] != "mailchimp" || m["list_id"] != "abc" || m["configured"] != true {
		t.Fatalf("%d %s", code, body)
	}
	if forms, _ := m["forms"].([]any); len(forms) != 1 || forms[0] != "contact" {
		t.Fatalf("forms %v", m["forms"])
	}
	if rows, _ := m["rows"].([]any); len(rows) != 1 {
		t.Fatalf("rows %v", m["rows"])
	}
	if strings.Contains(body, "SECRETKEY") || strings.Contains(body, "api_key") {
		t.Fatalf("key in %s", body)
	}
}

func TestLogRouteUnconfigured(t *testing.T) {
	routeHost(t, `{}`, 0, "", nil)
	_, m, _ := call(t, "GET", "/log")
	if m["configured"] != false {
		t.Fatalf("%v", m)
	}
	if forms, ok := m["forms"].([]any); !ok || len(forms) != 0 {
		t.Fatalf("forms %v", m["forms"])
	}
}

func TestTestRouteNotConfigured(t *testing.T) {
	routeHost(t, `{"provider":"brevo"}`, 0, "", nil)
	code, m, _ := call(t, "POST", "/test")
	if code != 200 || m["ok"] != false || m["error"] != "not configured" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestTestRouteSuccess(t *testing.T) {
	routeHost(t, baseSettings, 200, `{"name":"Newsletter"}`, nil)
	_, m, body := call(t, "POST", "/test")
	if m["ok"] != true || m["list_name"] != "Newsletter" || strings.Contains(body, "SECRETKEY") {
		t.Fatalf("%s", body)
	}
}

func TestTestRouteFailuresHaveFixedTextAndNoKey(t *testing.T) {
	for _, tc := range []struct {
		status int
		err    error
		want   string
	}{
		{401, nil, "the provider said 401"},
		{503, nil, "the provider said 503"},
		{0, &sdk.HostError{Code: sdk.ErrTimeout, Message: "SECRETKEY-us1 ada@example.com"}, "network error"},
	} {
		routeHost(t, baseSettings, tc.status, `{"detail":"SECRETKEY-us1 ada@example.com"}`, tc.err)
		_, m, body := call(t, "POST", "/test")
		if m["ok"] != false || m["error"] != tc.want {
			t.Errorf("%d: %s", tc.status, body)
		}
		if strings.Contains(body, "SECRETKEY") || strings.Contains(body, "@") {
			t.Errorf("leak in %s", body)
		}
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	routeHost(t, `{}`, 0, "", nil)
	if code, _, _ := call(t, "GET", "/nope"); code != 404 {
		t.Fatalf("%d", code)
	}
}
