package main

import (
	"net/url"
	"strings"
	"testing"
)

func decodeBar(t *testing.T, uri string) string {
	t.Helper()
	const prefix = "data:image/svg+xml;utf8,"
	if !strings.HasPrefix(uri, prefix) {
		t.Fatalf("not an svg data uri: %.40s", uri)
	}
	svg, err := url.PathUnescape(strings.TrimPrefix(uri, prefix))
	if err != nil {
		t.Fatal(err)
	}
	return svg
}

func TestBarHasNinetyCellsWithTheRightColours(t *testing.T) {
	var days [barDays]dayCell
	days[0] = dayCell{true, 100}
	days[1] = dayCell{true, 99.9}
	days[2] = dayCell{true, 99.89}
	days[3] = dayCell{true, 99}
	days[4] = dayCell{true, 98.99}
	days[5] = dayCell{true, 0}
	// days[6..] have no data
	svg := decodeBar(t, uptimeBar(days))
	if n := strings.Count(svg, "<rect "); n != barDays {
		t.Fatalf("%d cells", n)
	}
	var fills []string
	for _, part := range strings.Split(svg, `fill="`)[1:] {
		fills = append(fills, part[:7])
	}
	want := []string{colorGreen, colorGreen, colorAmber, colorAmber, colorRed, colorRed, colorGrey}
	for i, w := range want {
		if fills[i] != w {
			t.Fatalf("cell %d is %s, want %s", i, fills[i], w)
		}
	}
	if fills[barDays-1] != colorGrey {
		t.Fatalf("last cell %s", fills[barDays-1])
	}
}

func TestBarIsSafeInAnImgSource(t *testing.T) {
	uri := uptimeBar([barDays]dayCell{})
	if strings.ContainsAny(uri, "<>\"' #") {
		t.Fatalf("unescaped characters in %.80s", uri)
	}
	svg := decodeBar(t, uri)
	for _, bad := range []string{"<script", "style=", "<style", "href", "onload"} {
		if strings.Contains(svg, bad) {
			t.Fatalf("svg contains %q", bad)
		}
	}
}
