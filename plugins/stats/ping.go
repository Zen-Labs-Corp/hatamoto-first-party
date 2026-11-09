package main

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"regexp"
	"sort"
	"strconv"
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

var (
	// semverRe is a bare (no v prefix) semantic version without build metadata,
	// the form plugin and theme versions take.
	semverRe = regexp.MustCompile(`^(0|[1-9]\d*)\.(0|[1-9]\d*)\.(0|[1-9]\d*)(-[0-9A-Za-z.-]+)?$`)
	nameRe   = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,63}$`)
	osArchRe = regexp.MustCompile(`^[a-z0-9]{1,16}$`)
)

const (
	maxPlugins      = 100
	maxCount        = 1000000
	maxOtherPlugins = 1000
)

// detailedPing is the Detailed report of the payload contract.
type detailedPing struct {
	ping
	OS, Arch                string
	Postgres, AgeMonths     int
	Worker                  bool
	ThemeName, ThemeVersion string
	Plugins                 []pluginRow
	OtherPlugins            int
	Health                  health
}

// pluginRow is one first-party-signed plugin; Errors is its plugin_errors count.
type pluginRow struct {
	Name    string `json:"name"`
	Version string `json:"version"`
	Enabled bool   `json:"enabled"`
	Errors  int    `json:"errors"`
}

type health struct {
	SinceHours, Panics, HTTP5xx, RenderErrors int
	OtherErrors                               int
	UpdateFailed, UpdateRolledBack            int
	MigrationFailed                           int
}

var errInvalid = errors.New("invalid detailed ping")

// hasLevel reports whether the top-level object has a "level" key. It never
// fails: anything it cannot read is left to the parsers to reject.
func hasLevel(body string) bool {
	dec := json.NewDecoder(strings.NewReader(body))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return false
	}
	for dec.More() {
		kt, err := dec.Token()
		if key, ok := kt.(string); err != nil || !ok {
			return false
		} else if key == "level" {
			return true
		}
		var skip json.RawMessage
		if dec.Decode(&skip) != nil {
			return false
		}
	}
	return false
}

// parseDetailed validates a Detailed body token by token: every object, at
// every depth, has exactly its listed keys once each, in exact case, and every
// value has the right JSON type. It returns 0 when acceptable, 400 for
// invalid JSON or trailing data and 422 for any other violation.
func parseDetailed(body string) (detailedPing, int) {
	if !json.Valid([]byte(body)) {
		return detailedPing{}, http.StatusBadRequest
	}
	dec := json.NewDecoder(strings.NewReader(body))
	dec.UseNumber()
	var p detailedPing
	var level string
	var errs map[string]int
	err := readObject(dec, map[string]func() error{
		"install":    func() (err error) { p.Install, err = readString(dec); return },
		"version":    func() (err error) { p.Version, err = readString(dec); return },
		"channel":    func() (err error) { p.Channel, err = readString(dec); return },
		"level":      func() (err error) { level, err = readString(dec); return },
		"os":         func() (err error) { p.OS, err = readString(dec); return },
		"arch":       func() (err error) { p.Arch, err = readString(dec); return },
		"postgres":   func() (err error) { p.Postgres, err = readInt(dec, 9, 99); return },
		"age_months": func() (err error) { p.AgeMonths, err = readInt(dec, 0, 36); return },
		"worker":     func() (err error) { p.Worker, err = readBool(dec); return },
		"theme": func() error {
			return readObject(dec, map[string]func() error{
				"name":    func() (err error) { p.ThemeName, err = readString(dec); return },
				"version": func() (err error) { p.ThemeVersion, err = readString(dec); return },
			})
		},
		"plugins":       func() (err error) { p.Plugins, err = readPlugins(dec); return },
		"other_plugins": func() (err error) { p.OtherPlugins, err = readInt(dec, 0, maxOtherPlugins); return },
		"health":        func() (err error) { p.Health, errs, err = readHealth(dec); return },
	})
	if err != nil {
		return detailedPing{}, http.StatusUnprocessableEntity
	}
	if _, err := dec.Token(); err != io.EOF {
		return detailedPing{}, http.StatusBadRequest
	}
	if level != "detailed" || !installRe.MatchString(p.Install) || len(p.Version) > maxVersionLen || !versionRe.MatchString(p.Version) ||
		!osArchRe.MatchString(p.OS) || !osArchRe.MatchString(p.Arch) ||
		!nameRe.MatchString(p.ThemeName) || !validSemver(p.ThemeVersion) {
		return detailedPing{}, http.StatusUnprocessableEntity
	}
	switch p.Channel {
	case "stable", "edge", "canary":
	default:
		return detailedPing{}, http.StatusUnprocessableEntity
	}
	switch p.AgeMonths {
	case 0, 1, 3, 6, 12, 24, 36:
	default:
		return detailedPing{}, http.StatusUnprocessableEntity
	}
	// plugin_errors keys name a listed plugin or "other" (the sender's sum for
	// plugins it does not list).
	for key, n := range errs {
		if key == "other" {
			p.Health.OtherErrors = n
			continue
		}
		i := sort.Search(len(p.Plugins), func(i int) bool { return p.Plugins[i].Name >= key })
		if i == len(p.Plugins) || p.Plugins[i].Name != key {
			return detailedPing{}, http.StatusUnprocessableEntity
		}
		p.Plugins[i].Errors = n
	}
	return p, 0
}

func validSemver(v string) bool { return len(v) <= maxVersionLen && semverRe.MatchString(v) }

// readObject consumes one object whose keys are exactly those of fields, each
// once, calling the key's reader to consume its value.
func readObject(dec *json.Decoder, fields map[string]func() error) error {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errInvalid
	}
	seen := map[string]bool{}
	for dec.More() {
		kt, err := dec.Token()
		key, ok := kt.(string)
		if err != nil || !ok || seen[key] || fields[key] == nil {
			return errInvalid
		}
		seen[key] = true
		if err := fields[key](); err != nil {
			return err
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') || len(seen) != len(fields) {
		return errInvalid
	}
	return nil
}

func readString(dec *json.Decoder) (string, error) {
	tok, err := dec.Token()
	s, ok := tok.(string)
	if err != nil || !ok {
		return "", errInvalid
	}
	return s, nil
}

func readBool(dec *json.Decoder) (bool, error) {
	tok, err := dec.Token()
	b, ok := tok.(bool)
	if err != nil || !ok {
		return false, errInvalid
	}
	return b, nil
}

// readInt reads a JSON integer (no fraction or exponent) within [lo, hi].
func readInt(dec *json.Decoder, lo, hi int) (int, error) {
	tok, err := dec.Token()
	num, ok := tok.(json.Number)
	if err != nil || !ok {
		return 0, errInvalid
	}
	n, err := strconv.Atoi(num.String())
	if err != nil || n < lo || n > hi {
		return 0, errInvalid
	}
	return n, nil
}

// readPlugins reads at most maxPlugins entries, unique and sorted by name.
func readPlugins(dec *json.Decoder) ([]pluginRow, error) {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('[') {
		return nil, errInvalid
	}
	rows := []pluginRow{}
	for dec.More() {
		var r pluginRow
		err := readObject(dec, map[string]func() error{
			"name":    func() (err error) { r.Name, err = readString(dec); return },
			"version": func() (err error) { r.Version, err = readString(dec); return },
			"enabled": func() (err error) { r.Enabled, err = readBool(dec); return },
		})
		if err != nil || len(rows) == maxPlugins || !nameRe.MatchString(r.Name) || !validSemver(r.Version) ||
			(len(rows) > 0 && rows[len(rows)-1].Name >= r.Name) {
			return nil, errInvalid
		}
		rows = append(rows, r)
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim(']') {
		return nil, errInvalid
	}
	return rows, nil
}

// readHealth reads the health object and returns its plugin_errors counts by key.
func readHealth(dec *json.Decoder) (health, map[string]int, error) {
	var h health
	errs := map[string]int{}
	count := func(dst *int) func() error {
		return func() (err error) { *dst, err = readInt(dec, 0, maxCount); return }
	}
	err := readObject(dec, map[string]func() error{
		"since_hours":        func() (err error) { h.SinceHours, err = readInt(dec, 0, 10000); return },
		"panics":             count(&h.Panics),
		"http_5xx":           count(&h.HTTP5xx),
		"render_errors":      count(&h.RenderErrors),
		"update_failed":      count(&h.UpdateFailed),
		"update_rolled_back": count(&h.UpdateRolledBack),
		"migration_failed":   count(&h.MigrationFailed),
		"plugin_errors":      func() error { return readPluginErrors(dec, errs) },
	})
	return h, errs, err
}

// readPluginErrors reads an object of name -> count with at most maxPlugins
// distinct keys, each a plugin name or "other".
func readPluginErrors(dec *json.Decoder, out map[string]int) error {
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		return errInvalid
	}
	for dec.More() {
		kt, err := dec.Token()
		key, ok := kt.(string)
		if err != nil || !ok || len(out) == maxPlugins {
			return errInvalid
		}
		if _, dup := out[key]; dup || (key != "other" && !nameRe.MatchString(key)) {
			return errInvalid
		}
		if out[key], err = readInt(dec, 0, maxCount); err != nil {
			return err
		}
	}
	if tok, err := dec.Token(); err != nil || tok != json.Delim('}') {
		return errInvalid
	}
	return nil
}
