// Command payment-links is a Hatamoto plugin. Take payments and donations
// through Stripe Payment Links: payment buttons for your pages, an owner email
// per payment and a payments log.
//
// Logic stays in plain Go so it tests on the host; the //go:wasmexport lines
// live in exports_wasip1.go.
package main

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("prune", onPrune)
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "POST /webhook":
		return handleWebhook(r)
	}
	return status(http.StatusNotFound), nil
}

// onPrune fails open: a settings or database problem is logged, never returned.
func onPrune(sdk.CronEvent) error {
	s, err := loadSettings()
	if err != nil {
		logf("warn", "prune: settings unavailable")
		return nil
	}
	if err := prune(s.RetentionMonths); err != nil {
		logf("warn", "prune: database error")
	}
	return nil
}

// status answers with a code and an empty body.
func status(code int) sdk.HTTPResponse { return sdk.HTTPResponse{Status: code} }

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
