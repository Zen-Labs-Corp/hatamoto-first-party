package main

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	// A failed refresh is already stored and logged; the cron never reports it.
	guest.OnCron("refresh", func(sdk.CronEvent) error {
		_ = refreshAll(false)
		return nil
	})
}

// serve answers the owner routes. Every known route answers 200: a missing
// key or an unavailable db shows up as empty data, not as an error status.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /overview":
		return reply(http.StatusOK, buildOverview())
	case "POST /refresh-now":
		_ = refreshAll(true)
		return reply(http.StatusOK, buildOverview())
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
