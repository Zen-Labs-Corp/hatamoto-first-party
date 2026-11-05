// Command appointments is a Hatamoto plugin. Booking from Cal.com or Calendly: an embedded scheduler section and a booking button that use your scheduling link.
//
// The markup lives in partials/ and sections/. Templates cannot read the
// plugin's settings or validate a link, so five template funcs do it
// (embed_url, booking_url, button_label, height, provider_name). All of them
// fail open: any problem gives "" (or the default) and the template then
// renders nothing. The logic stays in plain Go (this file) and the
// //go:wasmexport lines in exports_wasip1.go, so everything but the exports
// builds and tests on the host.
package main

import (
	"encoding/json"
	"math"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"hatamoto.dev/sdk/guest"
)

const (
	maxLink      = 500
	maxLabel     = 60
	defaultLabel = "Book an appointment"
	minHeight    = 300
	maxHeight    = 2000
	defHeight    = 700

	providerCal      = "calcom"
	providerCalendly = "calendly"

	// settingsTTL is how long the resolved settings are reused. A render makes
	// several func calls; this keeps them to one settings.get.
	settingsTTL = 300 * time.Millisecond
)

// subdomain matches a host such as app.cal.com or a.b.cal.com.
var subdomain = regexp.MustCompile(`^([a-z0-9]([a-z0-9-]*[a-z0-9])?\.)+cal\.com$`)

// now is the clock (replaced in tests).
var now = time.Now

var cache struct {
	s       settings
	fetched time.Time
	ok      bool
}

// settings holds the raw value of each setting so one wrongly typed field
// does not discard the others.
type settings struct {
	Link   json.RawMessage `json:"link"`
	Label  json.RawMessage `json:"button_label"`
	Height json.RawMessage `json:"height"`
}

func init() {
	guest.OnFunc("embed_url", embedURL)
	guest.OnFunc("booking_url", bookingURL)
	guest.OnFunc("button_label", buttonLabel)
	guest.OnFunc("height", height)
	guest.OnFunc("provider_name", providerName)
}

// load reads the plugin's settings, reusing a read younger than settingsTTL.
// Any error (a missing grant, a host failure, bad JSON) gives empty settings;
// errors are not cached.
func load() settings {
	if t := now(); cache.ok && t.Sub(cache.fetched) >= 0 && t.Sub(cache.fetched) < settingsTTL {
		return cache.s
	}
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		cache.ok = false
		return settings{}
	}
	cache.s, cache.fetched, cache.ok = s, now(), true
	return s
}

func str(raw json.RawMessage) string {
	var v string
	if json.Unmarshal(raw, &v) != nil {
		return ""
	}
	return v
}

// arg returns the i-th template argument when it is a string, else "".
func arg(args []json.RawMessage, i int) string {
	if i >= len(args) {
		return ""
	}
	return str(args[i])
}

// validLink trims surrounding whitespace, then accepts only an https link on
// cal.com, a subdomain of cal.com or calendly.com, with a non-empty path, no
// userinfo, no port and no control characters. It returns the URL rebuilt from its parsed parts (no fragment)
// and the provider, never the raw input.
func validLink(raw string) (*url.URL, string, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > maxLink || strings.ContainsAny(raw, `\ `) {
		return nil, "", false
	}
	for _, r := range raw {
		if r < 0x20 || r == 0x7f {
			return nil, "", false
		}
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Opaque != "" || u.Port() != "" {
		return nil, "", false
	}
	if u.Path == "" || u.Path == "/" {
		return nil, "", false
	}
	var provider string
	switch h := u.Host; {
	case h == "cal.com" || subdomain.MatchString(h):
		provider = providerCal
	case h == "calendly.com":
		provider = providerCalendly
	default:
		return nil, "", false
	}
	clean := &url.URL{Scheme: "https", Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}
	return clean, provider, true
}

// embedURL returns the validated link with the provider's embed parameter.
// The first argument, when a non-empty string, overrides the setting; an
// invalid override gives "". A second argument "plain" returns the link
// without the embed parameter.
func embedURL(args []json.RawMessage) (string, error) {
	link := arg(args, 0)
	if link == "" {
		link = str(load().Link)
	}
	u, provider, ok := validLink(link)
	if !ok {
		return "", nil
	}
	if arg(args, 1) == "plain" {
		return u.String(), nil
	}
	q := u.Query()
	if provider == providerCal {
		q.Set("embed", "true")
	} else {
		q.Set("embed_type", "Inline")
	}
	u.RawQuery = q.Encode()
	return u.String(), nil
}

// bookingURL returns the link from the settings, else the (valid) fallback
// argument, else "". No embed parameter.
func bookingURL(args []json.RawMessage) (string, error) {
	for _, link := range []string{str(load().Link), arg(args, 0)} {
		if u, _, ok := validLink(link); ok {
			return u.String(), nil
		}
	}
	return "", nil
}

// buttonLabel returns the trimmed label setting, at most 60 characters, or
// the default.
func buttonLabel(_ []json.RawMessage) (string, error) {
	l := strings.TrimSpace(str(load().Label))
	if l == "" || strings.ContainsFunc(l, func(r rune) bool { return r < 0x20 || r == 0x7f }) {
		return defaultLabel, nil
	}
	if utf8.RuneCountInString(l) > maxLabel {
		l = strings.TrimSpace(string([]rune(l)[:maxLabel]))
	}
	return l, nil
}

// height returns the iframe height in pixels as decimal text, clamped to
// 300..2000, default 700.
func height(_ []json.RawMessage) (string, error) {
	var f float64
	if raw := load().Height; len(raw) == 0 || string(raw) == "null" || json.Unmarshal(raw, &f) != nil || math.IsNaN(f) || math.IsInf(f, 0) {
		return strconv.Itoa(defHeight), nil
	}
	return strconv.Itoa(int(math.Min(maxHeight, math.Max(minHeight, math.Trunc(f))))), nil
}

// providerName returns "Cal.com" or "Calendly" for the effective link (the
// first argument when a non-empty string, else the setting), else "".
func providerName(args []json.RawMessage) (string, error) {
	link := arg(args, 0)
	if link == "" {
		link = str(load().Link)
	}
	switch _, p, _ := validLink(link); p {
	case providerCal:
		return "Cal.com", nil
	case providerCalendly:
		return "Calendly", nil
	}
	return "", nil
}
