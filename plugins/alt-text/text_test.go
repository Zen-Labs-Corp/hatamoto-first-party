package main

import (
	"strings"
	"testing"
	"unicode/utf8"
)

func TestCleanAlt(t *testing.T) {
	cases := []struct {
		name, in string
		max      int
		want     string
	}{
		{"lead image of", "Image of a red car", 125, "A red car"},
		{"lead picture of", "picture of a dog.", 125, "A dog"},
		{"lead photo of lower", "photo of two birds", 125, "Two birds"},
		{"lead photograph of", "A photograph of a bridge", 125, "A bridge"},
		{"lead an image showing", "An image showing two cats.", 125, "Two cats"},
		{"lead the picture of", "The picture of a lake", 125, "A lake"},
		{"lead colon", "Image of: a tree", 125, "A tree"},
		{"mid sentence untouched", "Visitors take a photo of the harbour", 125, "Visitors take a photo of the harbour"},
		{"word not prefix", "Imagery of a forest", 125, "Imagery of a forest"},
		{"quotes", "\"A blue door\"", 125, "A blue door"},
		{"backticks and stars", "**`A blue door`**", 125, "A blue door"},
		{"multiline", "A blue\n  door\r\nwith a *handle*", 125, "A blue door with a *handle"},
		{"empty", "  ", 125, ""},
		{"only lead", "Image of", 125, ""},
		{"word boundary", "one two three four five", 14, "One two three"},
		{"exact fit", "one two three", 13, "One two three"},
		{"boundary on space", "one two three four", 13, "One two three"},
		{"no space hard cut", "abcdefghij", 5, "Abcde"},
		{"trailing punctuation", "one two, three", 9, "One two"},
		{"multibyte", "Café société élégante", 10, "Café"},
		{"emoji", "😀😀😀😀😀😀", 3, "😀😀😀"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got := cleanAlt(c.in, c.max)
			if got != c.want {
				t.Fatalf("cleanAlt(%q, %d) = %q, want %q", c.in, c.max, got, c.want)
			}
			if !utf8.ValidString(got) || utf8.RuneCountInString(got) > c.max {
				t.Fatalf("invalid or too long: %q", got)
			}
		})
	}
}

func TestCleanAltLongStaysWithinLimit(t *testing.T) {
	in := strings.Repeat("lorem ipsum ", 40)
	got := cleanAlt(in, 125)
	if n := utf8.RuneCountInString(got); n == 0 || n > 125 {
		t.Fatalf("length %d", n)
	}
	if strings.HasSuffix(got, " ") || !strings.HasSuffix(got, "ipsum") && !strings.HasSuffix(got, "lorem") {
		t.Fatalf("not cut at a word: %q", got)
	}
}

func TestIsDecorative(t *testing.T) {
	yes := []string{"DECORATIVE", "decorative", " Decorative. ", "\"DECORATIVE\"", "DECORATIVE - a divider"}
	no := []string{"", "A decorative vase", "Decorativeness", "Two cats"}
	for _, s := range yes {
		if !isDecorative(s) {
			t.Errorf("isDecorative(%q) = false", s)
		}
	}
	for _, s := range no {
		if isDecorative(s) {
			t.Errorf("isDecorative(%q) = true", s)
		}
	}
}

func TestClampMax(t *testing.T) {
	for in, want := range map[int]int{-5: 125, 0: 125, 1: 1, 125: 125, 300: 300, 301: 300, 9999: 300} {
		if got := clampMax(in); got != want {
			t.Errorf("clampMax(%d) = %d, want %d", in, got, want)
		}
	}
}
