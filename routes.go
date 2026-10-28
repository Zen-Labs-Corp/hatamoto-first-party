// Command stats is the first-party install stats receiver.
//
// It accepts the opt-in ping Hatamoto sends once a day, keeps one row per
// install and day (rolled up to one per month after 13 months) and shows the
// owner the totals. It stores only the install ID, version, channel and the UTC
// day; it logs nothing and reads no header except Content-Type.
package main

import (
	"encoding/json"
	"mime"
	"net/http"
	"strconv"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const maxPingBody = 1024

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("rollup", onRollup)
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "POST /v1/ping":
		return handlePing(r), nil
	case "GET /overview":
		return handleOverview(r)
	}
	return status(http.StatusNotFound), nil
}

// status answers with a code and an empty body.
func status(code int) sdk.HTTPResponse { return sdk.HTTPResponse{Status: code} }

// handlePing never returns a Go error: bad input is a 4xx and a database
// problem (including a missing db grant) is a 503, so the host does not count
// either as a plugin failure.
func handlePing(r sdk.HTTPRequest) sdk.HTTPResponse {
	if r.BodyB64 != "" {
		return status(http.StatusBadRequest)
	}
	if mt, _, err := mime.ParseMediaType(r.Headers["content-type"]); err != nil || mt != "application/json" {
		return status(http.StatusUnsupportedMediaType)
	}
	switch {
	case len(r.Body) == 0:
		return status(http.StatusBadRequest)
	case len(r.Body) > maxPingBody:
		return status(http.StatusRequestEntityTooLarge)
	}
	p, code := parsePing(r.Body)
	if code != 0 {
		return status(code)
	}
	if err := recordPing(p, utcDay()); err != nil {
		return status(http.StatusServiceUnavailable)
	}
	return status(http.StatusNoContent)
}

func handleOverview(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	days := 30
	if v := first(r.Query["days"]); v != "" {
		if v != "7" && v != "30" && v != "90" {
			return reply(http.StatusUnprocessableEntity, map[string]string{"error": "days must be 7, 30 or 90"})
		}
		days, _ = strconv.Atoi(v)
	}
	o, err := buildOverview(days)
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]string{"error": "the request failed"})
	}
	return reply(http.StatusOK, o)
}

func reply(code int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}
