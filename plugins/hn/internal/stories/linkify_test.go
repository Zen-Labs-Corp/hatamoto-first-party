package stories

import (
	"reflect"
	"testing"
)

func TestTextSegments(t *testing.T) {
	cases := []struct {
		in   string
		want []map[string]any
	}{
		{"see https://a.example/x.", []map[string]any{seg("see ", "", false), seg("https://a.example/x", "https://a.example/x", false), seg(".", "", false)}},
		{"(https://a.example/x)", []map[string]any{seg("(", "", false), seg("https://a.example/x", "https://a.example/x", false), seg(")", "", false)}},
		{"https://en.wikipedia.org/wiki/Foo_(bar)", []map[string]any{seg("https://en.wikipedia.org/wiki/Foo_(bar)", "https://en.wikipedia.org/wiki/Foo_(bar)", false)}},
		{"https://a.example/x),", []map[string]any{seg("https://a.example/x", "https://a.example/x", false), seg("),", "", false)}},
		{"http://x", []map[string]any{seg("http://x", "http://x", false)}},
		{"javascript:alert(1)", []map[string]any{seg("javascript:alert(1)", "", false)}},
		{"ftp://x", []map[string]any{seg("ftp://x", "", false)}},
		{"a\nb", []map[string]any{seg("a", "", false), seg("", "", true), seg("b", "", false)}},
		{"<b>https://a.example</b>", []map[string]any{seg("<b>", "", false), seg("https://a.example", "https://a.example", false), seg("</b>", "", false)}},
	}
	for _, c := range cases {
		if got := textSegments(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("textSegments(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}
