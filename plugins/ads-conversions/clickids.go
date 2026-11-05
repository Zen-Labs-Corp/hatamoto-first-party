package main

import (
	"net/url"
	"regexp"
	"strings"
)

// idRe is the shape of a click ID kept anywhere in the plugin.
var idRe = regexp.MustCompile(`^[A-Za-z0-9._~-]{1,256}$`)

// idKeys are the only click IDs the plugin knows.
var idKeys = []string{"gclid", "gbraid", "wbraid", "ttclid", "li_fat_id"}

// clickIDs maps an ID name from idKeys to its value.
type clickIDs map[string]string

// idsFromSourcePath reads the ad click IDs from the query of the page the
// visitor submitted from. The path is untrusted and may carry anything (even
// an email address), so only the five known keys survive and the rest is
// dropped; the URL itself is never returned.
func idsFromSourcePath(raw string) clickIDs {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 4096 {
		return nil
	}
	if i := strings.IndexByte(raw, '#'); i >= 0 {
		raw = raw[:i]
	}
	_, query, ok := strings.Cut(raw, "?")
	if !ok {
		return nil
	}
	q, _ := url.ParseQuery(query) // keeps the pairs that did parse
	out := clickIDs{}
	for _, k := range idKeys {
		if v := q.Get(k); idRe.MatchString(v) {
			out[k] = v
		}
	}
	return out
}

// cleanIDs keeps the valid values of the five known keys from a decoded JSON
// object; unknown keys and anything that is not a valid ID are dropped.
func cleanIDs(m map[string]any) clickIDs {
	out := clickIDs{}
	for _, k := range idKeys {
		if v, ok := m[k].(string); ok && idRe.MatchString(v) {
			out[k] = v
		}
	}
	return out
}

// mergeIDs returns base with over written on top.
func mergeIDs(base, over clickIDs) clickIDs {
	out := clickIDs{}
	for k, v := range base {
		out[k] = v
	}
	for k, v := range over {
		out[k] = v
	}
	return out
}

// maxPath caps the cleaned page path.
const maxPath = 512

// cleanPath reduces the client's source path to a safe path: an absolute
// http(s) URL loses its scheme, host, query and fragment, a plain path loses
// its query and fragment. Anything that is not a clean path with a leading
// "/" gives "".
func cleanPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r == '\\' {
			return ""
		}
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ""
		}
		raw = u.EscapedPath()
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || len(raw) > maxPath {
		return ""
	}
	return raw
}
