package main

import (
	"cmp"
	"regexp"
	"slices"
	"strings"
)

// Bucket is one labelled count of a split.
type Bucket struct {
	Label string `json:"label"`
	Count int    `json:"count"`
}

const (
	exactVersions = 3 // versions shown by name
	minorGroups   = 2 // vMAJOR.MINOR.x groups shown after them
	otherLabel    = "other"
)

var versionParts = regexp.MustCompile(`^v(\d+)\.(\d+)\.(\d+)(?:-(.*))?$`)

// minorOf maps v1.7.3 and v1.7.3-rc.1 to "v1.7.x"; anything else to "other".
func minorOf(v string) string {
	m := versionParts.FindStringSubmatch(v)
	if m == nil {
		return otherLabel
	}
	return "v" + m[1] + "." + m[2] + ".x"
}

// compareVersions orders versions numerically; a release sorts above its
// prereleases and prereleases compare as text.
func compareVersions(a, b string) int {
	ma, mb := versionParts.FindStringSubmatch(a), versionParts.FindStringSubmatch(b)
	if ma == nil || mb == nil {
		return strings.Compare(a, b)
	}
	for i := 1; i <= 3; i++ {
		if c := compareNumbers(ma[i], mb[i]); c != 0 {
			return c
		}
	}
	switch {
	case ma[4] == mb[4]:
		return 0
	case ma[4] == "":
		return 1
	case mb[4] == "":
		return -1
	}
	return strings.Compare(ma[4], mb[4])
}

// compareNumbers orders two digit strings of any length by value.
func compareNumbers(a, b string) int {
	a, b = strings.TrimLeft(a, "0"), strings.TrimLeft(b, "0")
	return cmp.Or(cmp.Compare(len(a), len(b)), strings.Compare(a, b))
}

// groupVersions applies the version rule: the 3 biggest versions by name (ties:
// the higher version first), then the 2 biggest minor groups of the rest, then
// everything else as "other". An input bucket labelled "other" (the SQL tail
// past its row cap) is never a version by name; it only adds to "other".
func groupVersions(in []Bucket) []Bucket {
	sorted := make([]Bucket, 0, len(in))
	other := 0
	for _, b := range in {
		if b.Label == otherLabel {
			other += b.Count
		} else {
			sorted = append(sorted, b)
		}
	}
	slices.SortFunc(sorted, func(a, b Bucket) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), compareVersions(b.Label, a.Label))
	})
	out := []Bucket{}
	if len(sorted) <= exactVersions {
		out = append(out, sorted...)
		if other > 0 {
			out = append(out, Bucket{otherLabel, other})
		}
		return out
	}
	out = append(out, sorted[:exactVersions]...)
	byMinor := map[string]int{}
	for _, b := range sorted[exactVersions:] {
		byMinor[minorOf(b.Label)] += b.Count
	}
	groups := make([]Bucket, 0, len(byMinor))
	for label, n := range byMinor {
		groups = append(groups, Bucket{label, n})
	}
	slices.SortFunc(groups, func(a, b Bucket) int {
		return cmp.Or(cmp.Compare(b.Count, a.Count), strings.Compare(b.Label, a.Label))
	})
	for i, g := range groups {
		if i < minorGroups && g.Label != otherLabel {
			out = append(out, g)
		} else {
			other += g.Count
		}
	}
	if other > 0 {
		out = append(out, Bucket{otherLabel, other})
	}
	return out
}

// channelBuckets returns stable, edge and canary in that order, zeros kept.
func channelBuckets(in []Bucket) []Bucket {
	out := []Bucket{{"stable", 0}, {"edge", 0}, {"canary", 0}}
	for _, b := range in {
		for i := range out {
			if out[i].Label == b.Label {
				out[i].Count += b.Count
			}
		}
	}
	return out
}
