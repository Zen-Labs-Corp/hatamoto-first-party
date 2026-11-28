package main

import (
	"cmp"
	"net/url"
	"slices"
	"strings"
)

// Directory states. stateOff exists for template parity only: the registry
// URLs are constants, so it is never produced.
const (
	stateOK          = "ok"
	stateEmpty       = "empty"
	stateUnavailable = "unavailable"
	stateOff         = "off"
)

// pluginCard is one plugin of the directory, as the theme reads it
// (.Content.cards). Cards are plain data; untrusted text stays text.
type pluginCard struct {
	Name        string     `json:"name"`
	Title       string     `json:"title"`
	Version     string     `json:"version"`
	Tier        string     `json:"tier"`
	TierLabel   string     `json:"tier_label"`
	Author      string     `json:"author"`
	Description string     `json:"description"`
	Source      string     `json:"source"`
	PermCount   int        `json:"perm_count"`
	PermWarn    []string   `json:"perm_warn"`
	PermLines   []permLine `json:"perm_lines"`
	Permissions []string   `json:"permissions"`
	Search      string     `json:"search"`
	About       []string   `json:"about"`
	Features    []string   `json:"features"`
}

// themeCard is one theme of the directory.
type themeCard struct {
	Name        string   `json:"name"`
	Title       string   `json:"title"`
	Version     string   `json:"version"`
	Description string   `json:"description"`
	Source      string   `json:"source"`
	Screenshot  string   `json:"screenshot"`
	Tier        string   `json:"tier"`
	TierLabel   string   `json:"tier_label"`
	Author      string   `json:"author"`
	Search      string   `json:"search"`
	About       []string `json:"about"`
	Features    []string `json:"features"`
}

type pluginDir struct {
	State string       `json:"state"`
	Cards []pluginCard `json:"cards"`
}

type themeDir struct {
	State string      `json:"state"`
	Cards []themeCard `json:"cards"`
}

// tierRank orders cards: first-party, community, then developer.
func tierRank(tier string) int {
	switch tier {
	case tierFirstParty:
		return 0
	case tierCommunity:
		return 1
	}
	return 2
}

// tierLabel is the label of a known tier and whether the tier shows its author.
func tierLabel(tier string) (label string, showAuthor bool) {
	switch tier {
	case tierFirstParty:
		return "First-party", false
	case tierCommunity:
		return "Community", true
	case tierDeveloper:
		return "Developer", true
	}
	return "", false
}

// buildDirs maps one index to both directory views: first-party first, then
// community, then developer, by name within each. A relative screenshot of a
// developer entry resolves against the developer index, any other against the
// main registry.
func buildDirs(idx *index) (pluginDir, themeDir) {
	var plugins []pluginCard
	var themes []themeCard
	for _, e := range idx.Packages {
		switch e.Kind {
		case kindPlugin:
			plugins = append(plugins, newPluginCard(e))
		case kindTheme:
			themes = append(themes, newThemeCard(e))
		}
	}
	slices.SortStableFunc(plugins, func(a, b pluginCard) int {
		return cmp.Or(cmp.Compare(tierRank(a.Tier), tierRank(b.Tier)), cmp.Compare(a.Name, b.Name))
	})
	slices.SortStableFunc(themes, func(a, b themeCard) int {
		return cmp.Or(cmp.Compare(tierRank(a.Tier), tierRank(b.Tier)), cmp.Compare(a.Name, b.Name))
	})
	pd, td := pluginDir{State: stateOK, Cards: plugins}, themeDir{State: stateOK, Cards: themes}
	if len(plugins) == 0 {
		pd.State = stateEmpty
	}
	if len(themes) == 0 {
		td.State = stateEmpty
	}
	return pd, td
}

func newPluginCard(e entry) pluginCard {
	c := pluginCard{
		Name: e.Name, Title: e.Title, Version: displayVersion(e.Version), Description: e.Description,
		Source: safeHTTPS(e.Source),
		Search: strings.ToLower(e.Title + " " + e.Name + " " + e.Description),
		About:  aboutParagraphs(e.About), Features: e.Features, Permissions: e.Permissions,
	}
	c.PermCount, c.PermWarn, c.PermLines = permCard(e.Permissions)
	if label, author := tierLabel(e.Tier); label != "" {
		c.Tier, c.TierLabel = e.Tier, label
		if author {
			c.Author = e.Author
		}
	}
	if e.Tier == tierDeveloper {
		c.Search += " " + strings.ToLower(e.Author)
	}
	return c
}

func newThemeCard(e entry) themeCard {
	base := registryURL
	if e.Tier == tierDeveloper {
		base = developerURL
	}
	c := themeCard{
		Name: e.Name, Title: e.Title, Version: displayVersion(e.Version), Description: e.Description,
		Source: safeHTTPS(e.Source), Screenshot: screenshotURL(base, e.Screenshot),
		Search: strings.ToLower(e.Title + " " + e.Name + " " + e.Description + " " + e.Author),
		About:  aboutParagraphs(e.About), Features: e.Features,
	}
	if label, author := tierLabel(e.Tier); label != "" {
		c.Tier, c.TierLabel = e.Tier, label
		if author {
			c.Author = e.Author
		}
	}
	return c
}

// aboutParagraphs splits the about text (paragraphs joined by a single newline).
func aboutParagraphs(about string) []string {
	if about == "" {
		return nil
	}
	return strings.Split(about, "\n")
}

// displayVersion shows a version as "v1.2.0" whether or not the index has the "v".
func displayVersion(v string) string {
	if v == "" || strings.HasPrefix(v, "v") {
		return v
	}
	return "v" + v
}

// safeHTTPS returns s when it is an https URL with a host and no credentials,
// and "" otherwise, so a hostile value never becomes a link.
func safeHTTPS(s string) string {
	u, err := url.Parse(strings.TrimSpace(s))
	if err != nil || u.Scheme != "https" || u.Host == "" || u.User != nil {
		return ""
	}
	return u.String()
}

// screenshotURL resolves a relative screenshot path against base and keeps it
// only when the result is a safe https URL.
func screenshotURL(base, rel string) string {
	if rel == "" {
		return ""
	}
	b, err := url.Parse(base)
	if err != nil {
		return ""
	}
	return safeHTTPS(b.ResolveReference(&url.URL{Path: rel}).String())
}
