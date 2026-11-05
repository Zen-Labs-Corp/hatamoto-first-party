package main

import "testing"

func TestHostCovered(t *testing.T) {
	cases := []struct {
		name  string
		url   string
		hosts []string
		want  bool
	}{
		{"exact", "https://images.pexels.com/a.jpg", []string{"images.pexels.com"}, true},
		{"other host", "https://evil.example/a.jpg", []string{"images.pexels.com"}, false},
		{"wildcard subdomain", "https://live.staticflickr.com/a.jpg", []string{"*.staticflickr.com"}, true},
		{"wildcard deep subdomain", "https://a.b.staticflickr.com/a.jpg", []string{"*.staticflickr.com"}, true},
		{"wildcard apex", "https://staticflickr.com/a.jpg", []string{"*.staticflickr.com"}, false},
		{"wildcard lookalike", "https://evilstaticflickr.com/a.jpg", []string{"*.staticflickr.com"}, false},
		{"port match", "https://h.example:8443/a.jpg", []string{"h.example:8443"}, true},
		{"port mismatch", "https://h.example:9443/a.jpg", []string{"h.example:8443"}, false},
		{"entry port, url default", "https://h.example/a.jpg", []string{"h.example:8443"}, false},
		{"no-port entry, odd port", "https://h.example:8443/a.jpg", []string{"h.example"}, false},
		{"no-port entry, explicit 443", "https://h.example:443/a.jpg", []string{"h.example"}, true},
		{"http default 80", "http://h.example:80/a.jpg", []string{"h.example"}, true},
		{"http with 443 only", "http://h.example:443/a.jpg", []string{"h.example"}, false},
		{"uppercase", "https://IMAGES.Pexels.COM/a.jpg", []string{"Images.Pexels.com"}, true},
		{"trailing dot", "https://images.pexels.com./a.jpg", []string{"images.pexels.com"}, true},
		{"userinfo", "https://user:pw@images.pexels.com/a.jpg", []string{"images.pexels.com"}, false},
		{"ftp", "ftp://images.pexels.com/a.jpg", []string{"images.pexels.com"}, false},
		{"malformed", "https://%zz/a.jpg", []string{"images.pexels.com"}, false},
		{"no host", "https:///a.jpg", []string{"images.pexels.com"}, false},
		{"empty hosts", "https://images.pexels.com/a.jpg", nil, false},
		{"empty entry", "https://images.pexels.com/a.jpg", []string{"", "*."}, false},
	}
	for _, c := range cases {
		if got := hostCovered(c.url, c.hosts); got != c.want {
			t.Errorf("%s: hostCovered(%q, %v) = %v, want %v", c.name, c.url, c.hosts, got, c.want)
		}
	}
}
