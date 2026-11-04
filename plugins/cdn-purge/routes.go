package main

import (
	"encoding/json"
	"net/http"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const notConfigured = "Choose a provider and add an API token in Settings."

func init() { guest.OnHTTP(serve) }

// serve answers the admin page. All routes are owner-only; none returns the
// API token, only whether one is set.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /log":
		return getLog()
	case "POST /purge-all":
		return postPurgeAll()
	case "POST /test":
		return postTest()
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

// getLog returns the settings summary and the recent purge log.
func getLog() (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	rows, err := listLog(logLimit)
	if err != nil {
		return fail(err)
	}
	return reply(http.StatusOK, map[string]any{
		"provider":   s.Provider,
		"target":     s.target(),
		"token_set":  s.APIToken != "",
		"configured": s.configured(),
		"rows":       rows,
	})
}

func postPurgeAll() (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	if !s.configured() {
		return invalid(notConfigured)
	}
	budgetEnd = time.Now().Add(hookBudget)
	r := purgeAll(s)
	insertLog("purge-all", s.Provider, nil, r.Status, r.Err)
	if !r.ok() {
		logf("error", "cdn-purge: %s purge-all failed: %s", s.Provider, r.Err)
	}
	return reply(http.StatusOK, map[string]any{"ok": r.ok(), "status": r.Status, "error": r.Err})
}

func postTest() (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	if !s.configured() {
		return invalid(notConfigured)
	}
	budgetEnd = time.Now().Add(hookBudget)
	status, msg := testConnection(s)
	return reply(http.StatusOK, map[string]any{"ok": status >= 200 && status <= 299, "status": status, "message": msg})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "cdn-purge: %v", err)
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

func invalid(msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusUnprocessableEntity, map[string]string{"error": msg})
}
