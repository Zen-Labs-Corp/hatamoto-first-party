package main

import (
	"encoding/json"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const (
	secSlack = "https://hooks.slack.com/services/T000/B000/SECRETSLACK"
	secTeams = "https://prod-12.westus.logic.azure.com:443/workflows/w/triggers/manual/paths/invoke?sig=SECRETSIG"
	secGChat = "https://chat.googleapis.com/v1/spaces/AAA/messages?key=SECRETKEY&token=SECRETGTOK"
	secToken = "123456:SECRETTELEGRAMTOKEN12345"
	secChat  = "-1001234"
)

var routeSettings = `{"slack_enabled":true,"slack_webhook_url":"` + secSlack + `",` +
	`"teams_enabled":false,"gchat_enabled":true,"gchat_webhook_url":"` + secGChat + `",` +
	`"telegram_enabled":true,"telegram_bot_token":"` + secToken + `","telegram_chat_id":"` + secChat + `",` +
	`"admin_url":"https://example.com/admin"}`

const routeForms = `{"items":[{"slug":"contact","name":"Contact","fields":[{"name":"name"},{"name":"message"}]},{"slug":"quote","name":"Quote","fields":[]}]}`

// routeHost answers settings, the log query, forms and http.fetch.
func routeHost(t *testing.T, settings, subs string, fetchStatus int, fetchBody string, fetchErr error) *fakeHost {
	t.Helper()
	h := &fakeHost{settings: settings, forms: routeForms, subs: subs, query: `[{"created_at":"2026-11-04T10:00:00Z","form_slug":"contact","channel":"slack","status":"sent","code":200}]`}
	h.install(t)
	inner := callHost
	callHost = func(fn string, a, out any) error {
		if fn != "http.fetch" {
			return inner(fn, a, out)
		}
		if fetchErr != nil {
			return fetchErr
		}
		b, _ := json.Marshal(map[string]any{"status": fetchStatus, "body": fetchBody})
		return json.Unmarshal(b, out)
	}
	return h
}

func call(t *testing.T, method, pattern, body string) (int, map[string]any, string) {
	t.Helper()
	res, err := serve(sdk.HTTPRequest{Method: method, Pattern: pattern, Body: body})
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var m map[string]any
	if err := json.Unmarshal([]byte(res.Body), &m); err != nil {
		t.Fatalf("body %q: %v", res.Body, err)
	}
	return res.Status, m, res.Body
}

const latestSub = `{"items":[{"id":"7","status":"new","data":{"name":"Ana","message":"Hello"}}],"total":1}`

func TestLogRouteShapeAndNoSecrets(t *testing.T) {
	routeHost(t, routeSettings, latestSub, 0, "", nil)
	code, m, body := call(t, "GET", "/log", "")
	if code != 200 {
		t.Fatalf("%d %s", code, body)
	}
	for _, s := range []string{"SECRETSLACK", "SECRETSIG", "SECRETKEY", "SECRETGTOK", "SECRETTELEGRAMTOKEN", "hooks.slack.com", "webhook_url", "bot_token"} {
		if strings.Contains(body, s) {
			t.Fatalf("%q in %s", s, body)
		}
	}
	ch := m["channels"].(map[string]any)
	if ch["slack"].(map[string]any)["set"] != true || ch["teams"].(map[string]any)["set"] != false {
		t.Fatalf("set flags: %v", ch)
	}
	if tg := ch["telegram"].(map[string]any); tg["chat_id"] != secChat || tg["enabled"] != true {
		t.Fatalf("telegram: %v", tg)
	}
	if rows, _ := m["rows"].([]any); len(rows) != 1 {
		t.Fatalf("rows %v", m["rows"])
	}
	if forms, _ := m["forms"].([]any); len(forms) != 2 {
		t.Fatalf("forms %v", m["forms"])
	}
}

func TestLogRoutePreview(t *testing.T) {
	h := routeHost(t, routeSettings, latestSub, 0, "", nil)
	_, m, body := call(t, "GET", "/log", "")
	p, _ := m["preview"].(map[string]any)
	if p == nil || p["form"] != "contact" || p["title"] != "New submission on contact" ||
		p["link"] != "https://example.com/admin/settings/submissions/7" {
		t.Fatalf("preview %s", body)
	}
	if pairs, _ := p["pairs"].([]any); len(pairs) != 2 {
		t.Fatalf("pairs %v", p["pairs"])
	}
	for _, c := range h.calls {
		if c.fn == "forms.submissions" && c.req["limit"] != 1 {
			t.Fatalf("limit %v", c.req["limit"])
		}
	}
}

func TestLogRoutePreviewNullWhenNoneOrSpam(t *testing.T) {
	for _, subs := range []string{`{"items":[],"total":0}`, `{"items":[{"id":"1","status":"spam","data":{"name":"x"}}]}`, ""} {
		routeHost(t, routeSettings, subs, 0, "", nil)
		_, m, body := call(t, "GET", "/log", "")
		if m["preview"] != nil {
			t.Fatalf("preview should be null: %s", body)
		}
	}
}

func TestLogRouteSettingsUnreadable(t *testing.T) {
	h := &fakeHost{}
	h.install(t)
	callHost = func(string, any, any) error { return errHost }
	if code, m, _ := call(t, "GET", "/log", ""); code != 503 || m["error"] != "settings unreadable" {
		t.Fatalf("%d %v", code, m)
	}
}

func TestTestRouteSlackOK(t *testing.T) {
	routeHost(t, routeSettings, "", 200, "ok", nil)
	code, m, body := call(t, "POST", "/test", `{"channel":"slack"}`)
	if code != 200 || m["ok"] != true || m["status"] != float64(200) || m["answer"] != "ok" {
		t.Fatalf("%d %s", code, body)
	}
}

func TestTestRouteTelegramAnswerReturned(t *testing.T) {
	routeHost(t, routeSettings, "", 400, `{"ok":false,"description":"Bad Request: chat not found"}`, nil)
	_, m, body := call(t, "POST", "/test", `{"channel":"telegram"}`)
	if m["ok"] != false || m["status"] != float64(400) || !strings.Contains(m["answer"].(string), "chat not found") {
		t.Fatalf("%s", body)
	}
}

func TestTestRouteNotSetUpAndUnknown(t *testing.T) {
	routeHost(t, routeSettings, "", 200, "ok", nil)
	if code, m, _ := call(t, "POST", "/test", `{"channel":"teams"}`); code != 200 || m["ok"] != false || m["error"] != "not set up" {
		t.Fatalf("%d %v", code, m)
	}
	if code, _, _ := call(t, "POST", "/test", `{"channel":"discord"}`); code != 400 {
		t.Fatalf("%d", code)
	}
	if code, _, _ := call(t, "POST", "/test", ``); code != 400 {
		t.Fatalf("%d", code)
	}
}

func TestTestRouteAnswerCappedAndStripped(t *testing.T) {
	long := secSlack + " " + strings.Repeat("x", 500)
	routeHost(t, routeSettings, "", 500, long, nil)
	_, m, body := call(t, "POST", "/test", `{"channel":"slack"}`)
	ans := m["answer"].(string)
	if len([]rune(ans)) > answerMax || strings.Contains(body, "SECRETSLACK") {
		t.Fatalf("%s", body)
	}
}

func TestTestRouteHostErrorsAreFixedWords(t *testing.T) {
	for code, want := range map[string]string{sdk.ErrTimeout: "timed out", sdk.ErrBlocked: "blocked", sdk.ErrInternal: "network error"} {
		routeHost(t, routeSettings, "", 0, "", &sdk.HostError{Code: code, Message: secSlack})
		_, m, body := call(t, "POST", "/test", `{"channel":"slack"}`)
		if m["ok"] != false || m["answer"] != want || strings.Contains(body, "SECRETSLACK") {
			t.Fatalf("%s: %s", code, body)
		}
	}
}

func TestSanitize(t *testing.T) {
	secrets := []string{secSlack, secTeams, secGChat, secToken, secChat}
	for _, tc := range []struct{ in, banned string }{
		{"bad url " + secSlack, "SECRETSLACK"},
		{"bad url " + secTeams, "SECRETSIG"},
		{"see https://x.example/y?key=SECRETK&other=1", "SECRETK"},
		{"see https://x.example/y?foo=1&token=SECRETT", "SECRETT"},
		{"sig=SECRETS", "SECRETS"},
		{"bot" + secToken + "/sendMessage not found", "SECRETTELEGRAMTOKEN"},
		{"chat " + secChat + " not found", secChat},
	} {
		if got := sanitize(tc.in, secrets...); strings.Contains(got, tc.banned) {
			t.Errorf("sanitize(%q) = %q", tc.in, got)
		}
	}
	if got := sanitize("Bad Request: chat not found", secrets...); got != "Bad Request: chat not found" {
		t.Errorf("plain text changed: %q", got)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	routeHost(t, `{}`, "", 0, "", nil)
	if code, _, _ := call(t, "GET", "/nope", ""); code != 404 {
		t.Fatalf("%d", code)
	}
}

func TestTestRouteSendsNoLinkForANonHTTPSAdminAddress(t *testing.T) {
	routeHost(t, strings.Replace(routeSettings, `"admin_url":"https://example.com/admin"`, `"admin_url":"javascript:alert(1)"`, 1), "", 200, "ok", nil)
	inner := callHost
	var sent string
	callHost = func(fn string, a, out any) error {
		if fn == "http.fetch" {
			b, _ := json.Marshal(a)
			sent = string(b)
		}
		return inner(fn, a, out)
	}
	call(t, "POST", "/test", `{"channel":"slack"}`)
	if sent == "" || strings.Contains(sent, "javascript") {
		t.Fatalf("request %s", sent)
	}
}
