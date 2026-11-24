package parity

import (
	"strings"
	"testing"
	"time"
)

func TestFavoritesHelpersParsePage(t *testing.T) {
	for in, want := range map[string]int{"": 1, "x": 1, "0": 1, "-3": 1, "1": 1, "7": 7, "100": 100, "101": 100, "99999999999999999999": 1} {
		if got := parsePage(in); got != want {
			t.Errorf("parsePage(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestFavoritesHelpersParseItemNo(t *testing.T) {
	for _, ok := range []string{"1", "42", "999999999999999999"} {
		if n, good := parseItemNo(ok); !good || n <= 0 {
			t.Errorf("parseItemNo(%q) refused", ok)
		}
	}
	for _, bad := range []string{"", "0", "-1", "abc", "1a", "+1", "1000000000000000000", " 1"} {
		if _, good := parseItemNo(bad); good {
			t.Errorf("parseItemNo(%q) accepted", bad)
		}
	}
}

func TestFavoritesHelpersIsUUID(t *testing.T) {
	if !isUUID("0b9a1f2e-3c4d-4e5f-8a6b-7c8d9e0f1a2b") {
		t.Error("canonical uuid refused")
	}
	for _, bad := range []string{"", "0B9A1F2E-3C4D-4E5F-8A6B-7C8D9E0F1A2B", "0b9a1f2e3c4d4e5f8a6b7c8d9e0f1a2b", "0b9a1f2e-3c4d-4e5f-8a6b-7c8d9e0f1a2b'", "1' OR '1'='1", strings.Repeat("a", 36)} {
		if isUUID(bad) {
			t.Errorf("isUUID(%q) accepted", bad)
		}
	}
}

func TestFavoritesHelpersLabels(t *testing.T) {
	if pointsLabel(1) != "1 point" || pointsLabel(3) != "3 points" || commentsLabel(0) != "discuss" || commentsLabel(1) != "1\u00a0comment" || commentsLabel(2) != "2\u00a0comments" {
		t.Error("labels")
	}
	for d, want := range map[time.Duration]string{time.Minute: "1 minute ago", 5 * time.Minute: "5 minutes ago", 90 * time.Minute: "1 hour ago", 3 * time.Hour: "3 hours ago", 30 * time.Hour: "1 day ago", 72 * time.Hour: "3 days ago"} {
		if got := ageText(d); got != want {
			t.Errorf("ageText(%v) = %q, want %q", d, got, want)
		}
	}
}
