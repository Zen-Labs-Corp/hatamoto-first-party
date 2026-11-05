package main

import (
	"net/url"
	"strings"
)

// hostCovered reports whether rawURL may be fetched with the run's hosts. It
// mirrors the host's own matcher: http/https only, no credentials in the URL,
// an exact host or a "*.suffix" entry (subdomains only, never the apex), an
// entry without a port matches only the scheme's default port.
func hostCovered(rawURL string, hosts []string) bool {
	u, err := url.Parse(rawURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil {
		return false
	}
	host := strings.TrimSuffix(strings.ToLower(u.Hostname()), ".")
	if host == "" {
		return false
	}
	port := u.Port()
	defaultPort := port == "" || (u.Scheme == "http" && port == "80") || (u.Scheme == "https" && port == "443")
	for _, entry := range hosts {
		entry = strings.ToLower(strings.TrimSpace(entry))
		pattern, wantPort, hasPort := strings.Cut(entry, ":")
		if hasPort {
			if wantPort != port {
				continue
			}
		} else if !defaultPort {
			continue
		}
		if suffix, ok := strings.CutPrefix(pattern, "*."); ok {
			if suffix != "" && strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if pattern != "" && pattern == host {
			return true
		}
	}
	return false
}
