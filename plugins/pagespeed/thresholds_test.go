package main

import "testing"

func TestCategoryBoundaries(t *testing.T) {
	tests := []struct {
		metric string
		v      float64
		want   string
	}{
		{"lcp", 2500, catGood}, {"lcp", 2501, catNeeds}, {"lcp", 4000, catNeeds}, {"lcp", 4001, catPoor},
		{"inp", 200, catGood}, {"inp", 201, catNeeds}, {"inp", 500, catNeeds}, {"inp", 501, catPoor},
		{"cls", 0.1, catGood}, {"cls", 0.11, catNeeds}, {"cls", 0.25, catNeeds}, {"cls", 0.26, catPoor},
		{"lcp", 0, catGood}, {"other", 1, ""},
	}
	for _, tc := range tests {
		if got := category(tc.metric, tc.v); got != tc.want {
			t.Errorf("category(%s, %v) = %q, want %q", tc.metric, tc.v, got, tc.want)
		}
	}
}
