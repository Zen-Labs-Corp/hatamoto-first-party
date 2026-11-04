package main

import (
	"encoding/json"
	"fmt"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnHTTP(serve) }

// serve answers the owner's admin page. A problem is a JSON answer, never a
// Go error (the host would turn that into a bare 502).
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /log":
		return logRoute()
	case "POST /test":
		return testRoute()
	}
	return reply(http.StatusNotFound, map[string]any{"error": "not found"}), nil
}

func reply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    string(b),
	}
}

// logRoute returns the settings summary (never the key) and the last 100 log rows.
func logRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "settings unreadable"}), nil
	}
	var rows json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb)
FROM (SELECT created_at, form_slug, status, reason, code FROM log ORDER BY id DESC LIMIT 100) t`, &rows)
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "delivery log unreadable"}), nil
	}
	forms := c.Forms
	if forms == nil {
		forms = []string{}
	}
	return reply(http.StatusOK, map[string]any{
		"provider":   c.Provider,
		"list_id":    c.ListID,
		"forms":      forms,
		"configured": c.configured(),
		"rows":       rows,
	}), nil
}

// testRoute checks the saved key and list with the provider.
func testRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "settings unreadable"}), nil
	}
	p := providerFor(c.Provider)
	if p == nil || !c.configured() {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "not configured"}), nil
	}
	name, r := p.test(c, doFetch)
	if r.kind == kindSent || r.kind == kindUpdated {
		return reply(http.StatusOK, map[string]any{"ok": true, "list_name": name}), nil
	}
	return reply(http.StatusOK, map[string]any{"ok": false, "error": testError(r)}), nil
}

// testError is a fixed short sentence for a failed check, never provider text.
func testError(r result) string {
	if r.status > 0 {
		return fmt.Sprintf("the provider said %d", r.status)
	}
	switch r.reason {
	case "network", "timeout":
		return "network error"
	case "":
		return "check failed"
	}
	return r.reason
}
