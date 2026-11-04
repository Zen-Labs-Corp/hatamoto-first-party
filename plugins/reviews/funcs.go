package main

import (
	"encoding/json"
	"strconv"
	"strings"

	"hatamoto.dev/sdk/guest"
)

const dateLayout = "2006-01-02"

func itoa(n int) string { return strconv.Itoa(n) }

func init() {
	guest.OnFunc("summary", funcSummary)
	guest.OnFunc("count", funcCount)
	guest.OnFunc("field", funcField)
	guest.OnFunc("profile", funcProfile)
}

// stars renders 1-5 as ★★★★☆.
func stars(n int) string {
	n = min(max(n, 0), 5)
	return strings.Repeat("★", n) + strings.Repeat("☆", 5-n)
}

// rating formats 4.80 as "4.8" and 5.00 as "5".
func rating(f float64) string {
	return strconv.FormatFloat(float64(int(f*10+0.5))/10, 'f', -1, 64)
}

// sourceArg reads the optional source argument; anything else is "" (all).
func sourceArg(args []json.RawMessage) string {
	if len(args) == 0 {
		return ""
	}
	var s string
	if json.Unmarshal(args[0], &s) != nil {
		return ""
	}
	if s = strings.ToLower(strings.TrimSpace(s)); s == sourceGoogle || s == sourceTrustpilot {
		return s
	}
	return ""
}

// hasData reports whether a source has a rating to show.
func hasData(i sourceInfo) bool { return !i.OKAt.IsZero() && i.Count > 0 && i.Rating > 0 }

func plural(n int) string {
	if n == 1 {
		return "review"
	}
	return "reviews"
}

// funcSummary: "4.8 from 132 Google reviews", with both sources
// "4.8 from 132 Google reviews, 4.6 from 210 on Trustpilot"; "" without data.
// Template funcs never fail: errors are an empty text.
func funcSummary(args []json.RawMessage) (string, error) {
	only := sourceArg(args)
	snap := current()
	var shown []string
	for _, src := range []string{sourceGoogle, sourceTrustpilot} {
		if (only == "" || only == src) && hasData(snap.Sources[src]) {
			shown = append(shown, src)
		}
	}
	var parts []string
	for _, src := range shown {
		i := snap.Sources[src]
		head := rating(i.Rating) + " from " + itoa(i.Count) + " "
		if len(shown) == 2 && src == sourceTrustpilot {
			parts = append(parts, head+"on "+sourceLabels[src])
		} else {
			parts = append(parts, head+sourceLabels[src]+" "+plural(i.Count))
		}
	}
	return strings.Join(parts, ", "), nil
}

// funcCount is the number of shown reviews, of one source when named; empty
// when there are none, so {{if plugin_reviews_count}} is false on no data and
// on a failed call alike.
func funcCount(args []json.RawMessage) (string, error) {
	only := sourceArg(args)
	n := 0
	for _, r := range current().Reviews {
		if only == "" || r.Source == only {
			n++
		}
	}
	if n == 0 {
		return "", nil
	}
	return itoa(n), nil
}

// funcField returns one field of the n-th shown review as text.
func funcField(args []json.RawMessage) (string, error) {
	if len(args) < 2 {
		return "", nil
	}
	var idx float64
	var key string
	if json.Unmarshal(args[0], &idx) != nil || json.Unmarshal(args[1], &key) != nil {
		return "", nil
	}
	rs := current().Reviews
	i := int(idx)
	if i < 0 || i >= len(rs) || float64(i) != idx {
		return "", nil
	}
	r := rs[i]
	switch key {
	case "author":
		return r.Author, nil
	case "author_url":
		return r.AuthorURL, nil
	case "photo":
		return r.PhotoURL, nil
	case "text":
		return r.Body, nil
	case "date":
		return r.Published.UTC().Format(dateLayout), nil
	case "rating":
		return itoa(r.Rating), nil
	case "stars":
		return stars(r.Rating), nil
	case "source":
		return r.Source, nil
	case "source_label":
		return sourceLabels[r.Source], nil
	case "url":
		return r.Link, nil
	}
	return "", nil
}

// funcProfile returns the profile or Maps URL of a source.
func funcProfile(args []json.RawMessage) (string, error) {
	if src := sourceArg(args); src != "" {
		return current().Sources[src].ProfileURL, nil
	}
	return "", nil
}
