package main

import (
	jsonv2 "encoding/json/v2"
	"fmt"
	"regexp"
	"slices"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

// Package kinds and tiers of an index entry.
const (
	kindPlugin = "plugin"
	kindTheme  = "theme"
	kindKit    = "kit"

	tierFirstParty = "first-party"
	tierCommunity  = "community"
	tierDeveloper  = "developer"
)

// firstPartyPlugins can never be shadowed by a developer entry (core origin.go).
var firstPartyPlugins = []string{"searchconsole"}

// Caps of about and features, as core's client sanitiser.
const (
	maxAbout      = 1500
	maxAboutParas = 10
	maxFeatures   = 8
	maxFeature    = 120
	maxReason     = 200
)

// Limits on a signed index's freshness.
const (
	maxIndexLifetime = 180 * 24 * time.Hour
	indexClockSkew   = 10 * time.Minute
)

var screenshotPathPattern = regexp.MustCompile(`^screenshots/[a-z][a-z0-9-]{1,39}\.(png|webp|jpg)$`)

// entry decodes only the members the pages use. Unknown members (size, tags,
// site_type, flags, category, ...) are ignored, so they can never fail an index.
type entry struct {
	Kind        string   `json:"kind"`
	Name        string   `json:"name"`
	Version     string   `json:"version"`
	Description string   `json:"description,omitempty"`
	URL         string   `json:"url"`
	SHA256      string   `json:"sha256"`
	Permissions []string `json:"permissions,omitempty"`
	Title       string   `json:"title,omitempty"`
	Author      string   `json:"author,omitempty"`
	Source      string   `json:"source,omitempty"`
	Tier        string   `json:"tier,omitempty"`
	SignerKey   string   `json:"signer_key,omitempty"`
	Screenshot  string   `json:"screenshot,omitempty"`
	About       string   `json:"about,omitempty"`
	Features    []string `json:"features,omitempty"`
}

// revocation withdraws a signer key, a package version or every version of a name.
type revocation struct {
	SignerKey string `json:"signer_key,omitempty"`
	Name      string `json:"name,omitempty"`
	Version   string `json:"version,omitempty"`
	Reason    string `json:"reason,omitempty"`
}

// index is the payload of a registry.json envelope.
type index struct {
	Version   int          `json:"version"`
	IssuedAt  string       `json:"issued_at"`
	ExpiresAt string       `json:"expires_at"`
	Packages  []entry      `json:"packages"`
	Revoked   []revocation `json:"revoked,omitempty"`
}

// checkFresh refuses a window that is malformed, longer than 180 days, issued
// in the future (beyond a 10 minute skew) or already expired at now.
func checkFresh(issuedAt, expiresAt string, now time.Time) error {
	issued, err1 := time.Parse(time.RFC3339, issuedAt)
	expires, err2 := time.Parse(time.RFC3339, expiresAt)
	if err1 != nil || err2 != nil || !expires.After(issued) || expires.Sub(issued) > maxIndexLifetime {
		return fmt.Errorf("registry index has no valid issued_at / expires_at (RFC 3339, at most %d days apart)", int(maxIndexLifetime.Hours()/24))
	}
	if issued.After(now.Add(indexClockSkew)) {
		return fmt.Errorf("registry index issued %s, in the future", issuedAt)
	}
	if !now.Before(expires) {
		return fmt.Errorf("registry index has expired (at %s)", expiresAt)
	}
	return nil
}

// parseIndex verifies a signed registry.json against t, checks version and
// freshness at now and drops malformed entries. Unsigned, tampered, expired or
// untrusted indexes are refused.
func parseIndex(doc []byte, t trustSet, now time.Time) (*index, error) {
	raw, err := t.verifyIndex(doc)
	if err != nil {
		return nil, err
	}
	var idx index
	if err := jsonv2.Unmarshal(raw, &idx, pinnedUnmarshal); err != nil {
		return nil, fmt.Errorf("registry index: %w", err)
	}
	if idx.Version != 1 {
		return nil, fmt.Errorf("registry index: unsupported version %d", idx.Version)
	}
	if err := checkFresh(idx.IssuedAt, idx.ExpiresAt, now); err != nil {
		return nil, err
	}
	valid := idx.Packages[:0]
	for _, e := range idx.Packages {
		if (e.Kind != kindPlugin && e.Kind != kindTheme && e.Kind != kindKit) || !sdk.NamePattern.MatchString(e.Name) || len(e.SHA256) != 64 || e.URL == "" {
			continue
		}
		if e.Tier != tierFirstParty && e.Tier != tierCommunity && e.Tier != tierDeveloper {
			e.Tier = ""
		}
		if _, err := parsePublicKey(e.SignerKey); err != nil {
			e.SignerKey = ""
		}
		if e.Tier == tierDeveloper && e.SignerKey == "" {
			continue
		}
		if !screenshotPathPattern.MatchString(e.Screenshot) {
			e.Screenshot = ""
		}
		e.About, e.Features = cleanAbout(e.About), cleanFeatures(e.Features)
		valid = append(valid, e)
	}
	idx.Packages = valid
	idx.Revoked = cleanRevocations(idx.Revoked)
	return &idx, nil
}

// hasControl reports whether s holds a control character other than keep.
func hasControl(s string, keep rune) bool {
	for _, r := range s {
		if unicode.IsControl(r) && r != keep {
			return true
		}
	}
	return false
}

// cleanAbout returns about, or "" when over the caps or holding a control
// character other than the paragraph newline.
func cleanAbout(about string) string {
	if utf8.RuneCountInString(about) > maxAbout || strings.Count(about, "\n")+1 > maxAboutParas || hasControl(about, '\n') {
		return ""
	}
	return about
}

// cleanFeatures keeps at most the first maxFeatures usable items.
func cleanFeatures(in []string) []string {
	var out []string
	for _, f := range in {
		if f == "" || utf8.RuneCountInString(f) > maxFeature || hasControl(f, -1) {
			continue
		}
		if out = append(out, f); len(out) == maxFeatures {
			break
		}
	}
	return out
}

// cleanRevocations keeps the usable revocations: one needs a parsable key or a
// package name. The reason is stripped of control characters and capped.
func cleanRevocations(in []revocation) []revocation {
	var out []revocation
	for _, rv := range in {
		if _, err := parsePublicKey(rv.SignerKey); err != nil {
			rv.SignerKey = ""
		}
		if !sdk.NamePattern.MatchString(rv.Name) {
			rv.Name, rv.Version = "", ""
		}
		if rv.SignerKey == "" && rv.Name == "" {
			continue
		}
		rv.Version = strings.TrimSpace(rv.Version)
		reason := strings.TrimSpace(strings.Map(func(c rune) rune {
			if unicode.IsControl(c) {
				return -1
			}
			return c
		}, rv.Reason))
		if runes := []rune(reason); len(runes) > maxReason {
			reason = string(runes[:maxReason])
		}
		rv.Reason = reason
		out = append(out, rv)
	}
	return out
}

// find returns the entry for kind and name.
func (idx *index) find(kind, name string) (entry, bool) {
	for _, e := range idx.Packages {
		if e.Kind == kind && e.Name == name {
			return e, true
		}
	}
	return entry{}, false
}

// filterDeveloper reduces the developer index to what may be offered: entries
// with a valid signer key, forced to the developer tier, minus every kind and
// name the main index lists (nil main: none) and every first-party plugin.
func filterDeveloper(dev, main *index) *index {
	out := &index{Version: dev.Version, IssuedAt: dev.IssuedAt, ExpiresAt: dev.ExpiresAt, Revoked: dev.Revoked}
	for _, e := range dev.Packages {
		if _, err := parsePublicKey(e.SignerKey); err != nil {
			continue
		}
		if slices.Contains(firstPartyPlugins, e.Name) {
			continue
		}
		if main != nil {
			if _, shadowed := main.find(e.Kind, e.Name); shadowed {
				continue
			}
		}
		e.Tier = tierDeveloper
		out.Packages = append(out.Packages, e)
	}
	return out
}

// revoked reports whether idx revokes the developer entry e: by its signer
// key, by its exact name and version, or by its name alone.
func revoked(idx *index, e entry) bool {
	if idx == nil {
		return false
	}
	var id string
	if pub, err := parsePublicKey(e.SignerKey); err == nil {
		id = keyID(pub)
	}
	for _, rv := range idx.Revoked {
		if rv.SignerKey != "" && id != "" {
			if pub, err := parsePublicKey(rv.SignerKey); err == nil && keyID(pub) == id {
				return true
			}
		}
		if rv.Name != "" && rv.Name == e.Name && (rv.Version == "" || rv.Version == e.Version) {
			return true
		}
	}
	return false
}
