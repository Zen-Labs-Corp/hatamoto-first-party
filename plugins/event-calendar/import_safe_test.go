package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestSafeText(t *testing.T) {
	for in, want := range map[string]string{
		"Doors: 7pm":                         "Doors: 7pm",
		"Start 19:30 sharp":                  "Start 19:30 sharp",
		"Tel: 020 7946 0000":                 "Tel: 020 7946 0000",
		"Tickets at https://x.example/a now": "Tickets at now",
		"See www.example.org":                "See",
		"Write mailto:a@b.example":           "Write",
		"Call tel:+44123":                    "Call",
		"Program flyer.pdf attached":         "Program attached",
		"Photo (cover.JPG).":                 "Photo",
		"Go /path here":                      "Go here",
		"a url(x) b":                         "a b",
		"[x](y)":                             "",
		"First line\nSecond line":            "First line\nSecond line",
		"and/or the 1.5 km walk":             "and/or the 1.5 km walk",
	} {
		if got := safeText(in); got != want {
			t.Errorf("safeText(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestSafeLink(t *testing.T) {
	for in, want := range map[string]string{
		"https://Tickets.Example.com/a":                "https://tickets.example.com/a",
		"HTTPS://tickets.example.com/a?b=c#d":          "https://tickets.example.com/a?b=c#d",
		"https://example.com/it's":                     "https://example.com/it%27s",
		"https://example.com/a%20b":                    "https://example.com/a%20b",
		"http://example.com/a":                         "",
		"https://example.com:8443/a":                   "",
		"https://192.0.2.1/a":                          "",
		"https://[2001:db8::1]/a":                      "",
		"https://user@example.com/a":                   "",
		"https://localhost/a":                          "",
		"https://example.com/logo.png":                 "",
		"https://example.com/a](b)":                    "",
		"https://example.com/?next=https://x.example/": "",
		"mailto:a@b.example":                           "",
		"//example.com/a":                              "",
		"":                                             "",
		"https://example.com/" + strings.Repeat("a", maxLinkLen): "",
	} {
		if got := safeLink(in); got != want {
			t.Errorf("safeLink(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestScheduledSlug(t *testing.T) {
	start := time.Date(2027, 6, 12, 20, 0, 0, 0, time.UTC)
	got := scheduledSlug("Open Mic Night & Jam!", start, "google-town-0123456789abcdef")
	if got != "open-mic-night-jam-2027-06-12-abcdef" {
		t.Errorf("slug %q", got)
	}
	long := scheduledSlug(strings.Repeat("word ", 40), start, "k-abcdef")
	if len(long) > maxSlugTitle+len("-2027-06-12-abcdef") || strings.Contains(long, "--") {
		t.Errorf("slug %q", long)
	}
	if got := scheduledSlug("!!!", start, "k-abcdef"); got != "event-2027-06-12-abcdef" {
		t.Errorf("slug %q", got)
	}
}

// ruleLink is a local copy of the rule list core applies to text outside the
// link fields, written apart from the code under test.
var ruleLink = regexp.MustCompile(`(?i)(://|\]\(|^[a-z][a-z0-9+.\-]*:\S|^[/\\]|^www\.|url\(|image-set\(|\.(jpe?g|png|gif|webp|svg|avif|mp3|mp4|mov|webm|pdf)$)`)

func TestSafeCreateLeavesNoLinkText(t *testing.T) {
	hostile := importEvent{
		Title:       "Jam www.example.org night",
		Description: "Info at https://example.org/a and mailto:x@y.example, poster.png /secret url(x)\nDoors: 7pm",
		Venue:       "Hall http://x.example", Address: "/Main Street", City: "Example City",
		TicketURL: "https://tickets.example.com/it's", OnlineURL: "http://insecure.example.com/", Website: "https://example.org/a.png",
	}
	attrs := buildCreateAttrs(hostile, time.UTC, "google", "k", "", "")
	attrs["price"], _ = json.Marshal("Free, see tel:+441")
	attrs["image"], _ = json.Marshal("x")
	out, name := safeCreate(attrs, hostile.Title)
	if _, bad := out["image"]; bad {
		t.Error("image kept")
	}
	if got := attrText(t, out, "ticket_url"); got != "https://tickets.example.com/it%27s" {
		t.Errorf("ticket_url %q", got)
	}
	for _, k := range []string{"online_url", "website"} {
		if _, kept := out[k]; kept {
			t.Errorf("%s kept: %s", k, out[k])
		}
	}
	check := map[string]string{"name": name}
	for k, v := range out {
		if k == "ticket_url" || k == "online_url" || k == "website" {
			continue
		}
		var s string
		if json.Unmarshal(v, &s) == nil {
			check[k] = s
		}
	}
	for k, s := range check {
		for _, tok := range strings.Fields(s) {
			if ruleLink.MatchString(strings.TrimRight(tok, ".,;:!?)")) {
				t.Errorf("%s keeps %q", k, tok)
			}
		}
	}
	if !strings.Contains(check["description"], "Doors: 7pm") || name != "Jam night" {
		t.Errorf("name %q, description %q", name, check["description"])
	}
}
