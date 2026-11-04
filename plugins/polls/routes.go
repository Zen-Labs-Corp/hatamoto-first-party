package main

import (
	"encoding/json"
	"errors"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnHTTP(serve) }

// serve routes by the manifest pattern. It never returns a Go error for bad
// input or a host problem: the host would turn that into a 502 and count it as
// a plugin failure, so those are plain 4xx and 5xx replies.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /results/{form_slug}":
		return handlePublic(r)
	case "GET /overview":
		return handleOverview(r)
	case "GET /forms/{slug}/csv":
		return handleCSV(r)
	case "POST /forms/{slug}/recount":
		return handleRecount(r)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(code int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: code, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func fail(code int, msg string) (sdk.HTTPResponse, error) {
	return reply(code, map[string]string{"error": msg})
}

// findCfg returns the configured form with this slug.
func findCfg(cfgs []formCfg, slug string) (formCfg, bool) {
	for _, c := range cfgs {
		if c.Slug == slug {
			return c, true
		}
	}
	return formCfg{}, false
}

func handleRecount(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	cfgs, err := loadConfig()
	if err != nil {
		_ = logf("warn", "polls: recount: settings unreadable")
		return fail(http.StatusInternalServerError, "settings unreadable")
	}
	if err := purge(cfgs); err != nil {
		_ = logf("warn", "polls: purge failed: %v", err)
	}
	cfg, ok := findCfg(cfgs, r.PathValues["slug"])
	if !ok {
		return fail(http.StatusNotFound, "form not set up")
	}
	n, err := recount(cfg)
	if err != nil {
		_ = logf("warn", "polls: recount of %q failed: %v", cfg.Slug, err)
		if errors.Is(err, errRecountIncomplete) {
			return fail(http.StatusGatewayTimeout, "Recount ran out of time before it finished. The form has too many submissions to recount within the time limit of a plugin route (about 8 seconds); the tallies were not changed.")
		}
		return fail(http.StatusInternalServerError, "recount failed")
	}
	return reply(http.StatusOK, map[string]any{"ok": true, "responses": n})
}
