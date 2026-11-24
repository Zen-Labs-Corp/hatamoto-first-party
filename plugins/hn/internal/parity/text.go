package parity

import (
	"regexp"
	"strconv"
	"time"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const maxPage = 100 // ?p= is clamped to 1..maxPage
const pageSize = 30 // search, favorites, RSS and the JSON API

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// isUUID reports whether s is a lower-case canonical uuid.
func isUUID(s string) bool { return uuidRe.MatchString(s) }

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
