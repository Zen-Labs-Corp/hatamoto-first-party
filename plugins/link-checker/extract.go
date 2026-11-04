package main

import (
	"encoding/json"
	"html"
	"net/url"
	"slices"
	"strings"
)

// extractLinks returns the distinct links in an entity's attributes, in
// document order (object keys sorted). It walks every string recursively:
// anchors inside HTML strings count, and so do the values of the keys url,
// href and link when they look like a link. Images (src) are not links here.
// Links under an exclude prefix are dropped.
func extractLinks(attrs json.RawMessage, origin string, exclude []string) []linkRef {
	var doc any
	if len(attrs) == 0 || json.Unmarshal(attrs, &doc) != nil {
		return nil
	}
	var out []linkRef
	seen := map[string]bool{}
	add := func(raw string) {
		ref, ok := normalise(raw, origin)
		if !ok || seen[ref.URL] || excluded(ref, origin, exclude) {
			return
		}
		seen[ref.URL] = true
		out = append(out, ref)
	}
	walk(doc, add)
	return out
}

func walk(v any, add func(string)) {
	switch x := v.(type) {
	case string:
		anchors(x, add)
	case []any:
		for _, e := range x {
			walk(e, add)
		}
	case map[string]any:
		keys := make([]string, 0, len(x))
		for k := range x {
			keys = append(keys, k)
		}
		slices.Sort(keys)
		for _, k := range keys {
			if s, ok := x[k].(string); ok && linkKey(k) && linkLike(s) {
				add(s)
			}
			walk(x[k], add)
		}
	}
}

func linkKey(k string) bool {
	switch strings.ToLower(k) {
	case "url", "href", "link":
		return true
	}
	return false
}

func linkLike(s string) bool {
	s = strings.TrimSpace(s)
	l := strings.ToLower(s)
	return strings.HasPrefix(l, "http://") || strings.HasPrefix(l, "https://") || strings.HasPrefix(s, "/")
}

// anchors calls add with the href of every <a ...> tag in s. It is a small
// scanner, not an HTML parser: it reads attribute names and quoted or bare
// values up to the closing '>' and ignores everything else.
func anchors(s string, add func(string)) {
	for i := 0; ; {
		j := strings.IndexByte(s[i:], '<')
		if j < 0 {
			return
		}
		i += j + 1
		if i+1 >= len(s) || (s[i] != 'a' && s[i] != 'A') || !isSpace(s[i+1]) {
			continue
		}
		i++
		var href string
		var found bool
		href, found, i = hrefOf(s, i)
		if found {
			add(href)
		}
	}
}

// hrefOf scans the attributes of a tag starting at i and returns the value of
// href and the index after the tag.
func hrefOf(s string, i int) (string, bool, int) {
	var href string
	var found bool
	for i < len(s) {
		for i < len(s) && (isSpace(s[i]) || s[i] == '/') {
			i++
		}
		if i >= len(s) || s[i] == '>' {
			return href, found, min(i+1, len(s))
		}
		start := i
		for i < len(s) && !isSpace(s[i]) && s[i] != '=' && s[i] != '>' && s[i] != '/' {
			i++
		}
		name := strings.ToLower(s[start:i])
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		if i >= len(s) || s[i] != '=' {
			continue
		}
		i++
		for i < len(s) && isSpace(s[i]) {
			i++
		}
		var val string
		switch {
		case i < len(s) && (s[i] == '"' || s[i] == '\''):
			q := s[i]
			end := strings.IndexByte(s[i+1:], q)
			if end < 0 {
				return href, found, len(s)
			}
			val = s[i+1 : i+1+end]
			i += end + 2
		default:
			start := i
			for i < len(s) && !isSpace(s[i]) && s[i] != '>' {
				i++
			}
			val = s[start:i]
		}
		if name == "href" && !found {
			href, found = val, true
		}
	}
	return href, found, i
}

func isSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\f'
}

// normalise turns a raw href into a link. Internal links are site paths with
// the query kept and the fragment removed; same-origin absolute links become
// internal. Outbound links are absolute http(s) addresses with a lowercase
// scheme and host. Anything else (mailto, tel, javascript, data, fragments,
// relative paths without a leading slash, other schemes) is not a link.
func normalise(raw, origin string) (linkRef, bool) {
	raw = strings.TrimSpace(html.UnescapeString(raw))
	if raw == "" || raw[0] == '#' {
		return linkRef{}, false
	}
	if strings.HasPrefix(raw, "//") {
		raw = "https:" + raw
	}
	u, err := url.Parse(raw)
	if err != nil {
		return linkRef{}, false
	}
	u.Fragment, u.RawFragment = "", ""
	switch strings.ToLower(u.Scheme) {
	case "":
		if u.Host != "" || !strings.HasPrefix(u.Path, "/") {
			return linkRef{}, false
		}
		return linkRef{URL: u.RequestURI(), Kind: kindInternal}, true
	case "http", "https":
		if u.Host == "" {
			return linkRef{}, false
		}
		u.Scheme = strings.ToLower(u.Scheme)
		u.Host = strings.ToLower(u.Host)
		if u.Path == "" {
			u.Path = "/"
		}
		if origin != "" && u.Scheme+"://"+u.Host == origin {
			return linkRef{URL: u.RequestURI(), Kind: kindInternal}, true
		}
		return linkRef{URL: u.String(), Kind: kindOutbound}, true
	}
	return linkRef{}, false
}

// excluded reports whether a link starts with one of the exclude prefixes. An
// internal link also matches an absolute prefix on the site's own origin.
func excluded(ref linkRef, origin string, exclude []string) bool {
	for _, p := range exclude {
		if strings.HasPrefix(ref.URL, p) {
			return true
		}
		if ref.Kind == kindInternal && origin != "" && strings.HasPrefix(origin+ref.URL, p) {
			return true
		}
	}
	return false
}

// siteOrigin is the scheme and host of the site, taken from an entity's public
// url and path ("" when the url has no origin, as when no public address is
// configured).
func siteOrigin(pageURL, path string) string {
	if pageURL == "" || pageURL == path {
		return ""
	}
	base := strings.TrimSuffix(strings.TrimRight(pageURL, "/"), strings.TrimRight(path, "/"))
	u, err := url.Parse(base)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.Path != "" || u.RawQuery != "" {
		return ""
	}
	return strings.ToLower(u.Scheme) + "://" + strings.ToLower(u.Host)
}
