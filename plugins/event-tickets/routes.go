package main

import (
	"encoding/json"
	"io"
	"net/http"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() { guest.OnHTTP(serve) }

// serve answers the routes. It never returns a Go error and never puts host
// or database error text in a body.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	// Core mounts GET routes, which also match HEAD; it drops the body of a
	// HEAD answer itself.
	if r.Method == http.MethodGet || r.Method == http.MethodHead {
		switch r.Pattern {
		case "/admin/events":
			return serveEvents(r.User, r.Query), nil
		case "/admin/events/{id}":
			return serveEventDetail(r.User, r.PathValues["id"]), nil
		case "/admin/settings":
			return serveSettings(r.User), nil
		}
	}
	if r.Method == http.MethodPost {
		switch r.Pattern {
		case "/admin/events/{id}/types":
			return serveSaveType(r, r.PathValues["id"]), nil
		case "/admin/types/{id}/delete":
			return serveDeleteType(r.User, r.PathValues["id"]), nil
		case "/admin/events/{id}/fields":
			return serveSaveFields(r, r.PathValues["id"]), nil
		}
	}
	return plain(http.StatusNotFound, "not found"), nil
}

func plain(code int, body string) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8"},
		Body:    body,
	}
}

func jsonReply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json; charset=utf-8", "Cache-Control": "no-store"},
		Body:    string(b),
	}
}

// failReply is the body of every JSON error.
func failReply(code int, msg string) sdk.HTTPResponse {
	return jsonReply(code, map[string]any{"ok": false, "error": msg})
}

func forbidden() sdk.HTTPResponse   { return failReply(http.StatusForbidden, "not allowed") }
func unavailable() sdk.HTTPResponse { return failReply(http.StatusServiceUnavailable, "unavailable") }

const maxBodyBytes = 16 << 10

// decodeBody reads a JSON body of at most 16 KiB into v and refuses unknown
// keys and trailing data. It returns an error message, empty when the body is fine.
func decodeBody(r sdk.HTTPRequest, v any) string {
	if r.BodyB64 != "" || len(r.Body) > maxBodyBytes {
		return "bad request"
	}
	dec := json.NewDecoder(strings.NewReader(r.Body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return "bad request"
	}
	if _, err := dec.Token(); err != io.EOF {
		return "bad request"
	}
	return ""
}

// queryValue is the first value of a query parameter. Core's bridge turns a
// non-string value the page sends into its JSON text, so an array arrives as
// `["x"]`; its first element is taken.
func queryValue(v []string) string {
	s := first(v)
	if strings.HasPrefix(s, "[") {
		var list []string
		if json.Unmarshal([]byte(s), &list) == nil {
			return first(list)
		}
	}
	return s
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return strings.TrimSpace(v[0])
}
