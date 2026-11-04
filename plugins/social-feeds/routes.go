package main

import (
	"encoding/json"
	"html"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	s := loadSettings()
	switch r.Method + " " + r.Pattern {
	case "GET /connect/instagram":
		return connectInstagram(s, r)
	case "GET /oauth/callback":
		return oauthCallback(s, r)
	case "GET /status":
		return reply(http.StatusOK, buildStatus(s, nil))
	case "POST /refresh-now":
		return reply(http.StatusOK, buildStatus(s, runRefresh(refreshNowBudget)))
	case "POST /disconnect":
		return disconnect(s)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

// fail logs err (redacted) and answers 500 without details.
func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "%s", redact(err.Error()))
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

func invalid(msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusUnprocessableEntity, map[string]string{"error": msg})
}

// page is a small HTML document for the browser tabs of the OAuth flow (the
// host serves plugin HTML sandboxed, without scripts).
func page(status int, title, body string, headers map[string]string) sdk.HTTPResponse {
	doc := "<!doctype html><meta charset=utf-8><title>" + html.EscapeString(title) + "</title><body style=\"font:16px system-ui;margin:3em auto;max-width:34em\"><h1>" +
		html.EscapeString(title) + "</h1>" + body
	h := map[string]string{"Content-Type": "text/html; charset=utf-8", "Cache-Control": "no-store"}
	for k, v := range headers {
		h[k] = v
	}
	return sdk.HTTPResponse{Status: status, Headers: h, Body: doc}
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
