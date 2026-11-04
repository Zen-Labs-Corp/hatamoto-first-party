package main

import (
	"errors"
	"net/url"
	"strings"

	"hatamoto.dev/sdk"
)

// botTokens are matched case-insensitively against the User-Agent. The match
// is deliberately crude: a false positive only means one download not counted.
var botTokens = []string{
	"bot", "crawl", "spider", "slurp", "preview", "facebookexternalhit",
	"curl", "wget", "python", "go-http-client", "headless", "monitor", "scanner",
}

// isBot reports an empty User-Agent or one containing a bot token.
func isBot(ua string) bool {
	ua = strings.ToLower(strings.TrimSpace(ua))
	if ua == "" {
		return true
	}
	for _, t := range botTokens {
		if strings.Contains(ua, t) {
			return true
		}
	}
	return false
}

// safeLocation mirrors the host's rule for a redirect target: a same-site
// path without spaces or control bytes. The host answers 502 for anything
// else, so it is checked here and the route answers 404 instead.
func safeLocation(p string) bool {
	if !strings.HasPrefix(p, "/") || strings.HasPrefix(p, "//") || strings.HasPrefix(p, "/\\") {
		return false
	}
	for i := 0; i < len(p); i++ {
		if p[i] <= 0x20 || p[i] == 0x7f {
			return false
		}
	}
	u, err := url.Parse(p)
	return err == nil && u.Scheme == "" && u.Host == "" && u.Opaque == ""
}

func notFound() sdk.HTTPResponse {
	return sdk.HTTPResponse{Status: 404, Headers: map[string]string{"Content-Type": "text/plain", "Cache-Control": "no-store"}, Body: "not found"}
}

func unavailable() sdk.HTTPResponse {
	return sdk.HTTPResponse{Status: 503, Headers: map[string]string{"Content-Type": "text/plain", "Cache-Control": "no-store"}, Body: "unavailable"}
}

// countsBots reads the setting; any error means the default (off).
func countsBots() bool {
	var s struct {
		CountBots bool `json:"count_bots"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return false
	}
	return s.CountBots
}

// handleDownload counts one download and redirects to the file. It never
// returns a Go error, and counting fails open: the redirect is answered even
// when the database or the settings are unavailable. Nothing about the
// visitor is stored or logged; the User-Agent is only inspected.
func handleDownload(r sdk.HTTPRequest) sdk.HTTPResponse {
	id := strings.ToLower(r.PathValues["media_id"])
	if !isUUID(id) {
		return notFound()
	}
	m, err := getMedia(id)
	if err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) && he.Code == sdk.ErrNotFound {
			return notFound()
		}
		return unavailable()
	}
	if m.PublicURL == nil || !safeLocation(*m.PublicURL) {
		return notFound()
	}
	if r.Method != "HEAD" && (!isBot(r.Headers["user-agent"]) || countsBots()) {
		_ = countDownload(id)
	}
	return sdk.HTTPResponse{Status: 302, Headers: map[string]string{
		"Location":      *m.PublicURL,
		"Cache-Control": "no-store",
	}}
}
