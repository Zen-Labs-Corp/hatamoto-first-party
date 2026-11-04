package main

import (
	"encoding/json"
	"net/http"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
	guest.OnFilter(sdk.BuilderData, onBuilderData)
	guest.OnCron("rollup", onRollup)
	guest.OnFunc("count", funcCount)
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /f/{media_id}", "HEAD /f/{media_id}":
		return handleDownload(r), nil
	case "GET /overview":
		return handleOverview(r)
	}
	return sdk.HTTPResponse{Status: http.StatusNotFound}, nil
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
