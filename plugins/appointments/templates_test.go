package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"os"
	"reflect"
	"regexp"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const (
	buttonFile     = "partials/button.html"
	buttonSiteFile = "partials/button_site.html"
	sectionFile    = "sections/appointments.html"
	buttonName     = "plugin/appointments/button"
	buttonSiteName = "plugin/appointments/button_site"
	sectionName    = "section-appointments"

	calLink      = "https://cal.com/acme/30min"
	calendlyLink = "https://calendly.com/acme/talk"
)

// The dots mirror core's field names: a field missing here is an execution
// error, as it is in the real engine. A section dot has no Site.
type sectionDot struct {
	Title   string
	Content map[string]any
	Page    struct{ Path string }
}

type pageDot struct {
	Site struct{ Org struct{ BookingURL string } }
}

func siteDot(booking string) pageDot {
	var d pageDot
	d.Site.Org.BookingURL = booking
	return d
}

func readFile(t *testing.T, name string) string {
	t.Helper()
	b, err := os.ReadFile(name)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

type manifest struct {
	Name           string                     `json:"name"`
	Version        string                     `json:"version"`
	SDK            string                     `json:"sdk"`
	Permissions    []string                   `json:"permissions"`
	Funcs          []string                   `json:"funcs"`
	CSP            map[string][]string        `json:"csp"`
	Limits         map[string]int             `json:"limits"`
	SettingsSchema map[string]json.RawMessage `json:"settings_schema"`
}

func readManifest(t *testing.T) manifest {
	t.Helper()
	var m manifest
	if err := json.Unmarshal([]byte(readFile(t, "plugin.json")), &m); err != nil {
		t.Fatal(err)
	}
	return m
}

// stubFuncs declares only the manifest funcs, as core does for plugin
// templates: no theme funcs. Every func returns "" (a host-level failure).
func stubFuncs(m manifest) template.FuncMap {
	fm := template.FuncMap{}
	for _, f := range m.Funcs {
		fm["plugin_appointments_"+f] = func(...any) string { return "" }
	}
	return fm
}

// call turns template arguments into the JSON array a host call carries.
func call(fn func([]json.RawMessage) (string, error)) func(...any) string {
	return func(a ...any) string {
		var raw []json.RawMessage
		for _, v := range a {
			b, _ := json.Marshal(v)
			raw = append(raw, b)
		}
		s, _ := fn(raw)
		return s
	}
}

// realFuncs runs the plugin's own funcs against a fake settings.get.
func realFuncs(t *testing.T, fixture string) template.FuncMap {
	t.Helper()
	fakeSettings(t, fixture, nil)
	return template.FuncMap{
		"plugin_appointments_embed_url":     call(embedURL),
		"plugin_appointments_booking_url":   call(bookingURL),
		"plugin_appointments_button_label":  call(buttonLabel),
		"plugin_appointments_height":        call(height),
		"plugin_appointments_provider_name": call(providerName),
	}
}

func renderWith(t *testing.T, funcs template.FuncMap, name string, data any) string {
	t.Helper()
	src := readFile(t, buttonFile) + readFile(t, buttonSiteFile) + readFile(t, sectionFile)
	tpl, err := template.New("t").Funcs(funcs).Parse(src)
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return b.String()
}

func sectionWith(title string, content map[string]any) sectionDot {
	return sectionDot{Title: title, Content: content}
}

func empty(out string) bool { return strings.TrimSpace(out) == "" }

type el struct {
	Name  string
	Attrs map[string]string
	Text  string
}

var allowedElements = map[string]bool{"section": true, "div": true, "h2": true, "iframe": true, "p": true, "a": true}

var bannedElements = strings.Fields(`script style object embed link meta base form svg math use animate set
foreignobject noscript template textarea title xmp plaintext noembed noframes frame frameset applet portal dialog`)

var bannedAttrs = map[string]bool{"srcdoc": true, "formaction": true, "http-equiv": true, "xlink:href": true, "action": true}

// scan tokenizes out and returns its elements plus every banned construct
// found (a mirror of the rules core applies to plugin markup; the iframe is
// allowed because site.scripts is granted).
func scan(out string) (els []el, problems []string) {
	z := html.NewTokenizer(strings.NewReader(out))
	inEl := false
	for {
		tt := z.Next()
		switch tt {
		case html.ErrorToken:
			return
		case html.TextToken:
			if inEl {
				els[len(els)-1].Text += string(z.Text())
			}
		case html.EndTagToken:
			inEl = false
		case html.StartTagToken, html.SelfClosingTagToken:
			inEl = true
			name, hasAttr := z.TagName()
			e := el{Name: string(name), Attrs: map[string]string{}}
			for _, b := range bannedElements {
				if e.Name == b {
					problems = append(problems, "element "+e.Name)
				}
			}
			if !allowedElements[e.Name] {
				problems = append(problems, "unexpected element "+e.Name)
			}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				key, val := string(k), string(v)
				e.Attrs[key] = val
				if strings.HasPrefix(key, "on") || bannedAttrs[key] {
					problems = append(problems, "attribute "+key)
				}
				flat := strings.ToLower(strings.Map(func(r rune) rune {
					if r <= ' ' || r == 0x7f {
						return -1
					}
					return r
				}, val))
				for _, p := range []string{"javascript:", "data:", "vbscript:"} {
					if strings.HasPrefix(flat, p) {
						problems = append(problems, "value "+p+" in "+key)
					}
				}
				if strings.Contains(val, "#ZgotmplZ") {
					problems = append(problems, "filtered value in "+key)
				}
			}
			els = append(els, e)
		}
	}
}

func scanOK(t *testing.T, out string) []el {
	t.Helper()
	els, problems := scan(out)
	if len(problems) > 0 {
		t.Fatalf("banned markup %v in %s", problems, out)
	}
	return els
}

func find(els []el, name string) []el {
	var out []el
	for _, e := range els {
		if e.Name == name {
			out = append(out, e)
		}
	}
	return out
}

func TestParsesWithOnlyManifestFuncs(t *testing.T) {
	m := readManifest(t)
	for file, want := range map[string]string{buttonFile: buttonName, buttonSiteFile: buttonSiteName, sectionFile: sectionName} {
		tpl, err := template.New("t").Funcs(stubFuncs(m)).Parse(readFile(t, file))
		if err != nil {
			t.Fatalf("%s: %v", file, err)
		}
		var names []string
		for _, x := range tpl.Templates() {
			if x.Name() != "t" {
				names = append(names, x.Name())
			}
		}
		if !reflect.DeepEqual(names, []string{want}) {
			t.Errorf("%s defines %v, want [%s]", file, names, want)
		}
	}
	if _, err := template.New("t").Funcs(stubFuncs(m)).Parse(`{{lower "A"}}`); err == nil {
		t.Fatal("a theme func parsed: the guard does not work")
	}
}

func TestManifest(t *testing.T) {
	m := readManifest(t)
	if m.Name != "appointments" || m.Version != "0.1.1" {
		t.Errorf("name/version = %q %q", m.Name, m.Version)
	}
	gomod := regexp.MustCompile(`hatamoto\.dev/sdk v(\d+)\.(\d+)\.\d+`).FindStringSubmatch(readFile(t, "go.mod"))
	if gomod == nil {
		t.Fatal("go.mod has no hatamoto.dev/sdk requirement")
	}
	if m.SDK != "^0.18" || m.SDK != "^"+gomod[1]+"."+gomod[2] {
		t.Errorf("sdk range %q does not match go.mod sdk %s.%s (want ^0.18)", m.SDK, gomod[1], gomod[2])
	}
	if !reflect.DeepEqual(m.Permissions, []string{"settings", "site.scripts"}) {
		t.Errorf("permissions = %v", m.Permissions)
	}
	if !reflect.DeepEqual(m.Funcs, []string{"embed_url", "booking_url", "button_label", "height", "provider_name"}) {
		t.Errorf("funcs = %v", m.Funcs)
	}
	wantCSP := map[string][]string{"frame_src": {"https://cal.com", "https://*.cal.com", "https://calendly.com"}}
	if !reflect.DeepEqual(m.CSP, wantCSP) {
		t.Errorf("csp = %v", m.CSP)
	}
	if m.Limits["memory_pages"] == 0 || m.Limits["max_instances"] == 0 {
		t.Errorf("limits = %v", m.Limits)
	}
	var props map[string]map[string]any
	if err := json.Unmarshal(m.SettingsSchema["properties"], &props); err != nil {
		t.Fatal(err)
	}
	if len(props) != 4 {
		t.Errorf("settings_schema has %d properties, want 4", len(props))
	}
	num := func(prop, key string) float64 { v, _ := props[prop][key].(float64); return v }
	if num("link", "maxLength") != 500 || num("button_label", "maxLength") != 60 ||
		num("height", "minimum") != 300 || num("height", "maximum") != 2000 {
		t.Errorf("schema bounds: %v", props)
	}
	if !reflect.DeepEqual(props["provider"]["enum"], []any{"calcom", "calendly"}) {
		t.Errorf("provider enum = %v", props["provider"]["enum"])
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, "plugin.json")), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"hooks", "allowed_hosts", "site", "routes", "ui"} {
		if _, ok := raw[k]; ok {
			t.Errorf("manifest has %q", k)
		}
	}
	if strings.Contains(readFile(t, "plugin.json"), "writeOnly") {
		t.Error("no setting is secret")
	}
	if _, err := os.Stat("assets/plugin.js"); err == nil {
		t.Error("assets/plugin.js must not exist")
	}
}

func TestSchemaPatternMatchesValidLink(t *testing.T) {
	var props map[string]struct {
		Pattern string `json:"pattern"`
	}
	if err := json.Unmarshal(readManifest(t).SettingsSchema["properties"], &props); err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(props["link"].Pattern)
	for link, want := range map[string]bool{
		"": true, calLink: true, calendlyLink: true, "https://app.cal.com/x": true,
		"http://cal.com/x": false, "https://evil-cal.com/x": false, "https://cal.com.evil.com/x": false,
		"javascript:alert(1)": false,
		"https://cal.com/a b": false, "https://cal.com/a\tb": false, "https://cal.com/x ": false,
	} {
		if re.MatchString(link) != want {
			t.Errorf("pattern on %q = %v, want %v", link, !want, want)
		}
	}
}

func TestSectionRenders(t *testing.T) {
	for name, c := range map[string]struct {
		fixture, src, name, text string
	}{
		"cal setting":      {`{"link":"` + calLink + `"}`, calLink + "?embed=true", "Cal.com", ""},
		"calendly setting": {`{"link":"` + calendlyLink + `"}`, calendlyLink + "?embed_type=Inline", "Calendly", ""},
	} {
		t.Run(name, func(t *testing.T) {
			out := renderWith(t, realFuncs(t, c.fixture), sectionName, sectionWith("Book us", map[string]any{}))
			els := scanOK(t, out)
			frames := find(els, "iframe")
			if len(frames) != 1 {
				t.Fatalf("%d iframes in %s", len(frames), out)
			}
			f := frames[0].Attrs
			if f["src"] != c.src {
				t.Errorf("src = %q, want %q", f["src"], c.src)
			}
			for _, tok := range []string{"allow-scripts", "allow-same-origin", "allow-forms", "allow-popups", "allow-popups-to-escape-sandbox"} {
				if !strings.Contains(" "+f["sandbox"]+" ", " "+tok+" ") {
					t.Errorf("sandbox %q lacks %s", f["sandbox"], tok)
				}
			}
			if f["loading"] != "lazy" || f["title"] == "" || f["referrerpolicy"] != "strict-origin-when-cross-origin" || f["height"] != "700" {
				t.Errorf("iframe attrs = %v", f)
			}
			inner := find(els, "div")
			if len(inner) != 1 || inner[0].Attrs["class"] != "appointments__inner" {
				t.Errorf("want one appointments__inner wrapper, got %v", inner)
			}
			if o, c := strings.Index(out, `<div class="appointments__inner">`), strings.LastIndex(out, "</div>"); o < 0 || c < strings.Index(out, "</iframe>") || c < strings.LastIndex(out, "</a>") || o > strings.Index(out, "<iframe") {
				t.Errorf("wrapper does not enclose the content: %s", out)
			}
			if h := find(els, "h2"); len(h) != 1 || h[0].Text != "Book us" {
				t.Errorf("title = %v", h)
			}
			a := find(els, "a")
			if len(a) != 1 || a[0].Attrs["rel"] != "noopener" || a[0].Attrs["target"] != "_blank" ||
				a[0].Attrs["href"] != strings.SplitN(c.src, "?", 2)[0] || a[0].Text != "Book on "+c.name {
				t.Errorf("fallback link = %v", a)
			}
		})
	}
}

func TestSectionContentLinkOverridesSetting(t *testing.T) {
	f := realFuncs(t, `{"link":"`+calLink+`","height":900}`)
	out := renderWith(t, f, sectionName, sectionWith("", map[string]any{"link": calendlyLink, "height": 5}))
	els := scanOK(t, out)
	if got := find(els, "iframe")[0]; got.Attrs["src"] != calendlyLink+"?embed_type=Inline" || got.Attrs["height"] != "900" {
		t.Errorf("iframe = %v", got.Attrs)
	}
	if a := find(els, "a")[0]; a.Attrs["href"] != calendlyLink || a.Text != "Book on Calendly" {
		t.Errorf("fallback = %v", a)
	}
	if len(find(els, "h2")) != 0 {
		t.Error("empty title rendered an h2")
	}
}

func TestNothingWithoutValidLink(t *testing.T) {
	hostile := []any{"https://evil.com/x", "http://cal.com/x", "javascript:alert(1)", "https://cal.com.evil.com/x", 5, nil, true, []any{"a"}, map[string]any{"a": 1}}
	for _, fixture := range []string{`{}`, `{"link":""}`, `{"link":"https://evil.com/x"}`, `{"link":"http://cal.com/x"}`, `{"link":7}`} {
		f := realFuncs(t, fixture)
		if out := renderWith(t, f, sectionName, sectionWith("T", map[string]any{})); !empty(out) {
			t.Errorf("%s: empty section rendered %q", fixture, out)
		}
		if out := renderWith(t, f, sectionName, sectionWith("T", nil)); !empty(out) {
			t.Errorf("%s: nil content rendered %q", fixture, out)
		}
		for _, h := range hostile {
			if out := renderWith(t, f, sectionName, sectionWith("T", map[string]any{"link": h})); !empty(out) {
				t.Errorf("%s with link %v: %q", fixture, h, out)
			}
		}
		if out := renderWith(t, f, buttonName, struct{}{}); !empty(out) {
			t.Errorf("%s: button rendered %q", fixture, out)
		}
		if out := renderWith(t, f, buttonSiteName, siteDot("")); !empty(out) {
			t.Errorf("%s: button_site rendered %q", fixture, out)
		}
	}
}

func TestFuncFailureRendersNothing(t *testing.T) {
	f := stubFuncs(readManifest(t))
	if out := renderWith(t, f, sectionName, sectionWith("T", map[string]any{"link": calLink})); !empty(out) {
		t.Errorf("section: %q", out)
	}
	if out := renderWith(t, f, buttonName, struct{}{}); !empty(out) {
		t.Errorf("button: %q", out)
	}
	if out := renderWith(t, f, buttonSiteName, siteDot(calLink)); !empty(out) {
		t.Errorf("button_site: %q", out)
	}
}

func TestButton(t *testing.T) {
	f := realFuncs(t, `{"link":"`+calLink+`","button_label":"Meet us"}`)
	for name, dot := range map[string]any{"plain": struct{}{}, "section dot": sectionWith("T", nil), "page dot": siteDot("")} {
		a := find(scanOK(t, renderWith(t, f, buttonName, dot)), "a")
		if len(a) != 1 || a[0].Attrs["class"] != "appointments__button" || a[0].Attrs["rel"] != "noopener" ||
			a[0].Attrs["href"] != calLink || a[0].Text != "Meet us" {
			t.Errorf("%s: button = %v", name, a)
		}
	}
	f = realFuncs(t, `{"link":"`+calLink+`"}`)
	if a := find(scanOK(t, renderWith(t, f, buttonName, struct{}{})), "a"); len(a) != 1 || a[0].Text != defaultLabel {
		t.Errorf("default label: %v", a)
	}
}

func TestButtonSiteFallback(t *testing.T) {
	// The setting wins over the business profile.
	f := realFuncs(t, `{"link":"`+calLink+`"}`)
	if a := find(scanOK(t, renderWith(t, f, buttonSiteName, siteDot(calendlyLink))), "a"); len(a) != 1 || a[0].Attrs["href"] != calLink {
		t.Errorf("setting first: %v", a)
	}
	f = realFuncs(t, `{}`)
	a := find(scanOK(t, renderWith(t, f, buttonSiteName, siteDot(calendlyLink))), "a")
	if len(a) != 1 || a[0].Attrs["href"] != calendlyLink || a[0].Attrs["class"] != "appointments__button" {
		t.Errorf("business fallback: %v", a)
	}
	for _, bad := range []string{"javascript:alert(1)", "https://evil.com/book", "http://cal.com/x", "https://cal.com.evil.com/x", "mailto:a@b.c"} {
		if out := renderWith(t, f, buttonSiteName, siteDot(bad)); !empty(out) {
			t.Errorf("hostile fallback %q rendered %q", bad, out)
		}
	}
}

func TestHostileTitleAndLabel(t *testing.T) {
	for _, s := range []string{`"><script>alert(1)</script>`, `x" onmouseover="alert(1)`, `a&b<c`} {
		label, _ := json.Marshal(s)
		f := realFuncs(t, `{"link":"`+calLink+`","button_label":`+string(label)+`}`)
		scanOK(t, renderWith(t, f, sectionName, sectionWith(s, map[string]any{})))
		a := find(scanOK(t, renderWith(t, f, buttonName, struct{}{})), "a")
		if len(a) != 1 || a[0].Text != strings.TrimSpace(s) {
			t.Errorf("label %q rendered as %v", s, a)
		}
	}
}

func TestBannedMarkupScanner(t *testing.T) {
	for _, bad := range []string{
		`<svg></svg>`, `<script>1</script>`, `<style>a{}</style>`, `<a onclick="x">`, `<a href="JaVa script:alert(1)">`,
		`<a href=" data:text/html,x">`, `<a href="vbscript:x">`, `<form action="/x">`, `<iframe srcdoc="x">`, `<table>`,
	} {
		if _, problems := scan(bad); len(problems) == 0 {
			t.Errorf("scanner accepted %s", bad)
		}
	}
}

// The stylesheet is linked on every public page, so every class must carry the
// plugin prefix to stay clear of theme classes.
func TestClassNamesArePrefixed(t *testing.T) {
	const prefix = "appointments"
	ok := func(c string) bool {
		return c == prefix || strings.HasPrefix(c, prefix+"__") || strings.HasPrefix(c, prefix+"--")
	}
	actions := regexp.MustCompile(`\{\{.*?\}\}`)
	attr := regexp.MustCompile(`class="([^"]*)"`)
	seen := 0
	for _, f := range []string{buttonFile, buttonSiteFile, sectionFile} {
		for _, m := range attr.FindAllStringSubmatch(readFile(t, f), -1) {
			for _, c := range strings.Fields(actions.ReplaceAllString(m[1], "")) {
				seen++
				if !ok(c) {
					t.Errorf("%s: class %q lacks the %s prefix", f, c, prefix)
				}
			}
		}
	}
	sel := regexp.MustCompile(`\.([A-Za-z_][\w-]*)`)
	for _, rule := range strings.Split(readFile(t, "assets/plugin.css"), "}") {
		head, _, found := strings.Cut(rule, "{")
		if !found {
			continue
		}
		for _, m := range sel.FindAllStringSubmatch(head, -1) {
			seen++
			if !ok(m[1]) {
				t.Errorf("plugin.css: class %q lacks the %s prefix", m[1], prefix)
			}
		}
	}
	if seen < 10 {
		t.Errorf("only %d class tokens found: the check is not looking", seen)
	}
}
