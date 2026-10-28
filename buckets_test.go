package main

import (
	"fmt"
	"slices"
	"strings"
	"testing"
)

func TestMinorOf(t *testing.T) {
	for in, want := range map[string]string{
		"v1.7.3": "v1.7.x", "v1.7.3-rc.1": "v1.7.x", "v10.0.0-edge.5": "v10.0.x", "junk": "other",
	} {
		if got := minorOf(in); got != want {
			t.Errorf("minorOf(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestGroupVersions(t *testing.T) {
	cases := []struct {
		name string
		in   []Bucket
		want []Bucket
	}{
		{"empty", nil, []Bucket{}},
		{"one version", []Bucket{{"v1.0.0", 5}}, []Bucket{{"v1.0.0", 5}}},
		{"three or fewer stay exact", []Bucket{{"v1.0.0", 1}, {"v1.1.0", 9}, {"v1.0.1", 4}},
			[]Bucket{{"v1.1.0", 9}, {"v1.0.1", 4}, {"v1.0.0", 1}}},
		{"ties: higher version first", []Bucket{{"v1.9.0", 2}, {"v1.10.0", 2}, {"v1.2.0", 2}},
			[]Bucket{{"v1.10.0", 2}, {"v1.9.0", 2}, {"v1.2.0", 2}}},
		{"release above its prerelease on a tie", []Bucket{{"v1.2.0-rc.1", 3}, {"v1.2.0", 3}},
			[]Bucket{{"v1.2.0", 3}, {"v1.2.0-rc.1", 3}}},
		{"rest grouped by minor, prerelease into its minor",
			[]Bucket{{"v2.0.0", 50}, {"v1.9.0", 40}, {"v1.8.0", 30}, {"v1.7.2", 5}, {"v1.7.1", 4}, {"v1.6.0-rc.1", 3}, {"v1.5.0", 2}, {"v1.4.0", 1}},
			[]Bucket{{"v2.0.0", 50}, {"v1.9.0", 40}, {"v1.8.0", 30}, {"v1.7.x", 9}, {"v1.6.x", 3}, {"other", 3}}},
		{"exactly two minor groups, no other",
			[]Bucket{{"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.0", 2}, {"v1.6.0", 1}},
			[]Bucket{{"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.x", 2}, {"v1.6.x", 1}}},
		{"tail row only adds to other", []Bucket{{"v1.0.0", 5}, {"other", 7}},
			[]Bucket{{"v1.0.0", 5}, {"other", 7}}},
		{"tail row is never an exact version",
			[]Bucket{{"other", 90}, {"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.0", 2}},
			[]Bucket{{"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.x", 2}, {"other", 90}}},
		{"tail row joins the grouped other",
			[]Bucket{{"other", 10}, {"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.0", 4}, {"v1.6.0", 3}, {"v1.5.0", 2}},
			[]Bucket{{"v2.0.0", 9}, {"v1.9.0", 8}, {"v1.8.0", 7}, {"v1.7.x", 4}, {"v1.6.x", 3}, {"other", 12}}},
	}
	for _, c := range cases {
		in := slices.Clone(c.in)
		if got := groupVersions(c.in); !slices.Equal(got, c.want) {
			t.Errorf("%s: got %v, want %v", c.name, got, c.want)
		}
		if !slices.Equal(in, c.in) {
			t.Errorf("%s: input was modified", c.name)
		}
	}
}

func TestChannelBuckets(t *testing.T) {
	got := channelBuckets([]Bucket{{"canary", 2}, {"stable", 7}, {"weird", 9}})
	want := []Bucket{{"stable", 7}, {"edge", 0}, {"canary", 2}}
	if !slices.Equal(got, want) {
		t.Fatalf("got %v", got)
	}
	if got := channelBuckets(nil); !slices.Equal(got, []Bucket{{"stable", 0}, {"edge", 0}, {"canary", 0}}) {
		t.Fatalf("empty: %v", got)
	}
}

func TestGroupVersionsAlwaysSumsToTheInput(t *testing.T) {
	var in []Bucket
	want := 0
	for i := 0; i < 1500; i++ {
		in = append(in, Bucket{fmt.Sprintf("v1.%d.%d-rc.%d", i%40, i%7, i), 1 + i%5})
		want += 1 + i%5
	}
	in = append(in, Bucket{"other", 321})
	want += 321
	got := 0
	for _, b := range groupVersions(in) {
		got += b.Count
	}
	if got != want {
		t.Fatalf("bars add up to %d, want %d", got, want)
	}
}

func TestVersionQuerySumsTheTailIntoOther(t *testing.T) {
	for _, want := range []string{"row_number() OVER (ORDER BY count(*) DESC, version DESC)", "g.rn <= 1000", "ELSE 'other'", "sum(g.count)"} {
		if !strings.Contains(qVersions, want) {
			t.Errorf("qVersions lacks %q", want)
		}
	}
	if strings.Contains(qVersions, "LIMIT 1000") {
		t.Error("qVersions must not cut the tail")
	}
}

func TestCompareVersionsHandlesHugeNumbers(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{
		{"v99999999999999999999.0.0", "v99999999999999999998.0.0", 1},
		{"v99999999999999999999.0.0", "v9223372036854775807.0.0", 1},
		{"v1.99999999999999999999.0", "v1.99999999999999999999.0", 0},
		{"v1.0.100000000000000000000", "v1.0.99999999999999999999", 1},
		{"v1.10.0", "v1.9.0", 1},
		{"v1.007.0", "v1.7.0", 0},
		{"v0.0.0", "v0.0.0-rc.1", 1},
	} {
		if got := compareVersions(c.a, c.b); got != c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.a, c.b, got, c.want)
		}
		if got := compareVersions(c.b, c.a); got != -c.want {
			t.Errorf("compareVersions(%q, %q) = %d, want %d", c.b, c.a, got, -c.want)
		}
	}
}
