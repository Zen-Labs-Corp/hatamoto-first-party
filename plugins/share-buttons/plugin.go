// Command share-buttons is a Hatamoto plugin. Share links for posts and pages (LinkedIn, X, Facebook, WhatsApp, Bluesky, email) as a partial and a section, with no tracking scripts.
//
// The markup lives in partials/ and sections/. Templates cannot read the
// plugin's settings, so three template funcs do it (network, style, page_url).
// All of them fail open: any settings problem gives the defaults. The logic
// stays in plain Go (this file) and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"encoding/json"
	"math"
	"net/url"
	"strings"

	"hatamoto.dev/sdk/guest"
)

var (
	allNetworks     = []string{"linkedin", "x", "facebook", "whatsapp", "bluesky", "email"}
	defaultNetworks = []string{"linkedin", "x", "facebook", "whatsapp", "email"}
)

const (
	styleBoth  = "both"
	maxSiteURL = 200
)

// settings holds the raw value of each setting so one wrongly typed field
// does not discard the others.
type settings struct {
	Networks json.RawMessage `json:"networks"`
	Style    json.RawMessage `json:"style"`
	SiteURL  json.RawMessage `json:"site_url"`
}

func init() {
	guest.OnFunc("network", network)
	guest.OnFunc("style", style)
	guest.OnFunc("page_url", pageURL)
}

// load reads the plugin's settings once. Any error (a missing grant, a host
// failure, bad JSON) gives empty settings, which resolve to the defaults.
func load() settings {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return settings{}
	}
	return s
}

func str(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

// enabled returns the networks to show, in order: known ids only, lower-cased,
// first occurrence wins. An empty result means the defaults.
func enabled(s settings) []string {
	var in []string
	if json.Unmarshal(s.Networks, &in) != nil {
		return defaultNetworks
	}
	var out []string
	seen := map[string]bool{}
	for _, n := range in {
		n = strings.ToLower(strings.TrimSpace(n))
		if seen[n] || !known(n) {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	if len(out) == 0 {
		return defaultNetworks
	}
	return out
}

func known(n string) bool {
	for _, k := range allNetworks {
		if k == n {
			return true
		}
	}
	return false
}

// network returns the id of the i-th enabled network (0-based), "-" past the
// end and "" for a bad argument.
func network(args []json.RawMessage) (string, error) {
	if len(args) < 1 {
		return "", nil
	}
	var f float64
	if string(args[0]) == "null" || json.Unmarshal(args[0], &f) != nil || f != math.Trunc(f) || f < 0 || f > 1000 {
		return "", nil
	}
	list := enabled(load())
	if i := int(f); i < len(list) {
		return list[i], nil
	}
	return "-", nil
}

// style returns icons, labels or both (anything else gives both).
func style(_ []json.RawMessage) (string, error) {
	switch v := strings.ToLower(strings.TrimSpace(str(load().Style))); v {
	case "icons", "labels":
		return v, nil
	}
	return styleBoth, nil
}

// pageURL joins the configured site origin and a page path. Either part
// invalid gives "".
func pageURL(args []json.RawMessage) (string, error) {
	if len(args) < 1 {
		return "", nil
	}
	var path string
	if json.Unmarshal(args[0], &path) != nil || !validPath(path) {
		return "", nil
	}
	origin := siteOrigin(str(load().SiteURL))
	if origin == "" {
		return "", nil
	}
	return origin + path, nil
}

func validPath(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") {
		return false
	}
	for _, r := range p {
		if r < 0x20 || r == 0x7f || r == '\\' {
			return false
		}
	}
	return true
}

// siteOrigin returns scheme://host[:port] for an http(s) origin with at most
// one trailing slash, "" for anything else.
func siteOrigin(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxSiteURL {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil {
		return ""
	}
	if (u.Path != "" && u.Path != "/") || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" || u.Opaque != "" {
		return ""
	}
	return u.Scheme + "://" + u.Host
}
