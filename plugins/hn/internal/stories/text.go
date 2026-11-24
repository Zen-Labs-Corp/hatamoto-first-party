package stories

import (
	"math"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"golang.org/x/net/publicsuffix"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const maxText = 10000 // runes of a text post
const maxPage = 100   // ?p= is clamped to 1..maxPage

// kindOf is "ask" or "show" for a title starting (case-insensitively) with
// "Ask:" or "Show:", else "story".
func kindOf(title string) string {
	t := strings.ToLower(strings.TrimSpace(title))
	switch {
	case strings.HasPrefix(t, "ask:"):
		return "ask"
	case strings.HasPrefix(t, "show:"):
		return "show"
	}
	return "story"
}

// siteOf is the registrable host (eTLD+1) of a NormalizeURL domain; on error
// the domain itself, and "" stays "".
func siteOf(domain string) string {
	if domain == "" {
		return ""
	}
	site, err := publicsuffix.EffectiveTLDPlusOne(domain)
	if err != nil {
		return domain
	}
	return site
}

// gravity is (points-1)/(hours+2)^1.8 for a story of that age; a negative age counts as 0.
func gravity(points int, age time.Duration) float64 {
	h := math.Max(age.Hours(), 0)
	return float64(points-1) / math.Pow(h+2, 1.8)
}

// scoreSQL is the SQL of gravity: points is an integer expression, now a
// timestamptz placeholder such as "$3".
func scoreSQL(points, now string) string {
	return "(" + points + " - 1) / power(greatest(extract(epoch FROM (" + now + "::timestamptz - created_at)), 0) / 3600.0 + 2, 1.8)"
}

// ageText is a coarse "N minutes ago" label for an age.
func ageText(d time.Duration) string {
	switch {
	case d < 2*time.Minute:
		return "1 minute ago"
	case d < time.Hour:
		return strconv.Itoa(int(d/time.Minute)) + " minutes ago"
	case d < 2*time.Hour:
		return "1 hour ago"
	case d < 24*time.Hour:
		return strconv.Itoa(int(d/time.Hour)) + " hours ago"
	case d < 48*time.Hour:
		return "1 day ago"
	}
	return strconv.Itoa(int(d/(24*time.Hour))) + " days ago"
}

// pointsLabel is "1 point" or "N points".
func pointsLabel(n int) string {
	if n == 1 {
		return "1 point"
	}
	return strconv.Itoa(n) + " points"
}

// commentsLabel is "discuss", "1 comment" or "N comments" (with a no-break space).
func commentsLabel(n int) string {
	switch n {
	case 0:
		return "discuss"
	case 1:
		return "1\u00a0comment"
	}
	return strconv.Itoa(n) + "\u00a0comments"
}

// parsePage reads ?p=: invalid or below 1 is 1, above maxPage is maxPage.
func parsePage(s string) int {
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 {
		return 1
	}
	return min(n, maxPage)
}

// parseItemNo reads a route {id}: 1 to 18 digits, above zero.
func parseItemNo(s string) (int64, bool) {
	if s == "" || len(s) > 18 {
		return 0, false
	}
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, false
		}
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// nowArg is the clock as an SQL timestamptz argument.
func nowArg() string { return core.Now().UTC().Format(time.RFC3339Nano) }

// textOK reports whether a text post is within maxText characters.
func textOK(s string) bool { return utf8.RuneCountInString(strings.TrimSpace(s)) <= maxText }

// plainText reports whether s is valid UTF-8 without control characters (NUL
// included). Tab, newline and carriage return pass when layout is true.
func plainText(s string, layout bool) bool {
	if !utf8.ValidString(s) {
		return false
	}
	for _, c := range s {
		if unicode.IsControl(c) && !(layout && (c == '\t' || c == '\n' || c == '\r')) {
			return false
		}
	}
	return true
}
