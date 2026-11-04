package main

import (
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
	}
	return sdk.HTTPResponse{Status: http.StatusNotFound}, nil
}
