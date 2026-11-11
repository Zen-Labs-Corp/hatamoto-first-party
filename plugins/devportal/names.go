package main

import (
	_ "embed"
	"regexp"
	"strings"
)

//go:embed policy/reserved-names.txt
var reservedRaw string

//go:embed policy/first-party-names.txt
var firstPartyRaw string

var namePattern = regexp.MustCompile(`^[a-z][a-z0-9-]{1,39}$`)

// Categories and SiteTypes are the values core accepts for developer
// listings (plugin category, theme site type).
var Categories = []string{"SEO", "Analytics", "Marketing", "Forms", "Commerce", "Content", "Media", "Developer", "Security", "Integrations"}
var SiteTypes = []string{"blog", "business", "docs", "landing", "portfolio", "restaurant"}

// nameSet reads one name per line, ignoring blanks and '#' comments.
func nameSet(raw string) map[string]bool {
	out := map[string]bool{}
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

func reservedSet() map[string]bool { return nameSet(reservedRaw) }

// nameRefusal returns "" when the name may be submitted, else "invalid" or
// "reserved" (reserved or already a first-party or community package).
func nameRefusal(name string) string {
	if !namePattern.MatchString(name) {
		return "invalid"
	}
	if reservedSet()[name] || nameSet(firstPartyRaw)[name] {
		return "reserved"
	}
	return ""
}

// ownsOrFree reports whether the account may use a name: nobody owns it yet
// or the account already does.
func ownsOrFree(owner *int64, account int64) bool {
	return owner == nil || *owner == account
}
