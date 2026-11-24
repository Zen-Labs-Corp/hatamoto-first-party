package stories

import (
	"math"
	"strings"
	"testing"
	"time"
)

func TestTextKindOf(t *testing.T) {
	for in, want := range map[string]string{"Ask: x": "ask", "ask: x": "ask", "Show: x": "show", "Asking": "story", "  Show:y": "show", "plain": "story"} {
		if got := kindOf(in); got != want {
			t.Errorf("kindOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestTextSiteOf(t *testing.T) {
	for in, want := range map[string]string{"blog.github.com": "github.com", "example.co.uk": "example.co.uk", "notes.example.net": "example.net", "localhost": "localhost", "": ""} {
		if got := siteOf(in); got != want {
			t.Errorf("siteOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestRankGravityFormula(t *testing.T) {
	if g := gravity(1, 5*time.Hour); g != 0 {
		t.Errorf("gravity(1) = %v", g)
	}
	if g, want := gravity(10, 0), 9/math.Pow(2, 1.8); math.Abs(g-want) > 1e-9 {
		t.Errorf("gravity(10, 0) = %v, want %v", g, want)
	}
	if g, want := gravity(10, -time.Hour), gravity(10, 0); g != want {
		t.Errorf("negative age = %v, want %v", g, want)
	}
	if gravity(10, 10*time.Hour) >= gravity(10, time.Hour) {
		t.Error("older story must rank lower")
	}
	if s := scoreSQL("points + 1", "$3"); !strings.Contains(s, "(points + 1 - 1)") || !strings.Contains(s, "$3::timestamptz") || !strings.Contains(s, "1.8") {
		t.Errorf("scoreSQL = %s", s)
	}
}

func TestTextAgeText(t *testing.T) {
	for d, want := range map[time.Duration]string{
		0: "1 minute ago", 119 * time.Second: "1 minute ago", 2 * time.Minute: "2 minutes ago",
		59 * time.Minute: "59 minutes ago", time.Hour: "1 hour ago", 119 * time.Minute: "1 hour ago",
		2 * time.Hour: "2 hours ago", 23 * time.Hour: "23 hours ago", 24 * time.Hour: "1 day ago",
		47 * time.Hour: "1 day ago", 48 * time.Hour: "2 days ago", 10 * 24 * time.Hour: "10 days ago",
	} {
		if got := ageText(d); got != want {
			t.Errorf("ageText(%v) = %q, want %q", d, got, want)
		}
	}
}

func TestTextLabels(t *testing.T) {
	if pointsLabel(1) != "1 point" || pointsLabel(0) != "0 points" || pointsLabel(7) != "7 points" {
		t.Error("pointsLabel")
	}
	if commentsLabel(0) != "discuss" || commentsLabel(1) != "1\u00a0comment" || commentsLabel(3) != "3\u00a0comments" {
		t.Error("commentsLabel")
	}
}

func TestTextParsePage(t *testing.T) {
	for in, want := range map[string]int{"": 1, "0": 1, "-3": 1, "abc": 1, "2": 2, "9999": maxPage} {
		if got := parsePage(in); got != want {
			t.Errorf("parsePage(%q) = %d, want %d", in, got, want)
		}
	}
}

func TestTextParseItemNo(t *testing.T) {
	for in, want := range map[string]int64{"42": 42, "042": 42, "4a": 0, "": 0, "0": 0, "-1": 0, strings.Repeat("9", 20): 0} {
		got, ok := parseItemNo(in)
		if got != want || ok != (want != 0) {
			t.Errorf("parseItemNo(%q) = %d, %v", in, got, ok)
		}
	}
}

func TestTextTextOK(t *testing.T) {
	if !textOK(strings.Repeat("é", maxText)) || textOK(strings.Repeat("é", maxText+1)) {
		t.Error("textOK counts runes")
	}
}
