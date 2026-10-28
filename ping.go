package main

import (
	"encoding/json"
	"io"
	"net/http"
	"regexp"
	"strings"
)

var (
	installRe = regexp.MustCompile(`^[0-9a-f]{32}$`)
	// versionRe is the release version form Hatamoto core accepts everywhere
	// (core's release.canonicalVersion): vMAJOR.MINOR.PATCH with an optional
	// -prerelease and no build metadata.
	versionRe = regexp.MustCompile(`^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*)(\.(0|[1-9][0-9]*|[0-9]*[A-Za-z-][0-9A-Za-z-]*))*)?$`)
)

const maxVersionLen = 64

// parsePing validates the body strictly: a JSON object with exactly the keys
// install, version and channel (case-sensitive, once each), every value a
// string, nothing after the object. It returns 0 when the ping is acceptable,
// 400 for a malformed body and 422 for values that break a field rule.
func parsePing(body string) (ping, int) {
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return ping{}, http.StatusBadRequest
	}
	seen := map[string]string{}
	for dec.More() {
		kt, err := dec.Token()
		key, ok := kt.(string)
		if err != nil || !ok {
			return ping{}, http.StatusBadRequest
		}
		if key != "install" && key != "version" && key != "channel" {
			return ping{}, http.StatusBadRequest
		}
		if _, dup := seen[key]; dup {
			return ping{}, http.StatusBadRequest
		}
		var raw json.RawMessage
		if dec.Decode(&raw) != nil || len(raw) == 0 || raw[0] != '"' {
			return ping{}, http.StatusBadRequest // null, numbers, objects, arrays, booleans
		}
		var v string
		if json.Unmarshal(raw, &v) != nil {
			return ping{}, http.StatusBadRequest
		}
		seen[key] = v
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return ping{}, http.StatusBadRequest
	}
	if _, err := dec.Token(); err != io.EOF {
		return ping{}, http.StatusBadRequest // trailing data
	}
	if len(seen) != 3 {
		return ping{}, http.StatusBadRequest
	}
	p := ping{Install: seen["install"], Version: seen["version"], Channel: seen["channel"]}
	if !installRe.MatchString(p.Install) || len(p.Version) > maxVersionLen || !versionRe.MatchString(p.Version) {
		return ping{}, http.StatusUnprocessableEntity
	}
	switch p.Channel {
	case "stable", "edge", "canary":
	default:
		return ping{}, http.StatusUnprocessableEntity
	}
	return p, 0
}
