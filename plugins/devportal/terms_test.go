package main

import (
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestTermsEscapesEverything(t *testing.T) {
	md := "# Rules <b>\n\nSee [x](http://evil.example) and <script>alert(1)</script>\nsecond line & more\n\n# Next"
	out := renderTerms(md, `a"><img src=x onerror=1>@x.io`)
	for _, bad := range []string{"<script", "<b>", "<img", `href="http://evil`} {
		if strings.Contains(out, bad) {
			t.Errorf("output contains %q:\n%s", bad, out)
		}
	}
	for _, want := range []string{"<h2>Rules &lt;b&gt;</h2>", "&lt;script&gt;alert(1)&lt;/script&gt;", "second line &amp; more", "<h2>Next</h2>", "&lt;img src=x", "See [x](http://evil.example) and"} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q:\n%s", want, out)
		}
	}
	// The only link is the fixed one back to the portal.
	if strings.Count(out, "<a ") != 1 || !strings.Contains(out, `<a href="/developers">`) {
		t.Errorf("links in terms page:\n%s", out)
	}
}

func TestTermsEmptySettingUsesTheDraftAndOmitsContact(t *testing.T) {
	out := renderTerms("  \n ", "")
	if !strings.Contains(out, "<h2>Developer terms</h2>") || strings.Contains(out, "Contact:") {
		t.Errorf("%s", out)
	}
	if !strings.Contains(renderTerms("", "r@example.org"), "Contact: r@example.org") {
		t.Error("review contact not shown")
	}
}

func TestDraftMatchesTheManifestDefault(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var doc struct {
		Schema struct {
			Properties struct {
				Terms struct {
					Default string `json:"default"`
				} `json:"terms_markdown"`
			} `json:"properties"`
		} `json:"settings_schema"`
	}
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc.Schema.Properties.Terms.Default != defaultTerms {
		t.Error("defaultTerms differs from the plugin.json default")
	}
}

func TestTermsRouteServesHTMLFromSettings(t *testing.T) {
	h := newPortal(t)
	h.settings = `{"terms_markdown":"# Mine\n\n<i>x</i>","review_email":""}`
	resp, err := serve(sdkGet("/terms"))
	if err != nil || resp.Status != 200 || !strings.HasPrefix(resp.Headers["Content-Type"], "text/html") ||
		!strings.Contains(resp.Body, "<h2>Mine</h2>") || strings.Contains(resp.Body, "<i>") {
		t.Fatalf("%+v %v", resp, err)
	}
}
