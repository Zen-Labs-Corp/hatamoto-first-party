package main

import (
	"bytes"
	"encoding/json"
	"html/template"
	"net/url"
	"os"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/net/html"
)

const (
	partialFile = "partials/links.html"
	sectionFile = "sections/share_buttons.html"
	partialName = "plugin/share_buttons/links"
	sectionName = "section-share_buttons"
)

// The dots mirror core's field names: a field missing here is an execution
// error, as it is in the real engine.
type entity struct{ Title string }

type pageDot struct {
	Head   struct{ Canonical string }
	Entity *entity
	Page   struct{ Name string }
}

type sectionDot struct {
	Title   string
	Content map[string]any
	Page    struct{ Path string }
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
// templates: no theme funcs.
func stubFuncs(m manifest) template.FuncMap {
	fm := template.FuncMap{}
	for _, f := range m.Funcs {
		fm["plugin_share_buttons_"+f] = func(...any) string { return "" }
	}
	return fm
}

// realFuncs runs the plugin's own funcs against a fake settings.get.
func realFuncs(t *testing.T, fixture string) template.FuncMap {
	t.Helper()
	fakeSettings(t, fixture, nil)
	return template.FuncMap{
		"plugin_share_buttons_network": func(i int) string {
			s, _ := network([]json.RawMessage{json.RawMessage(strconv.Itoa(i))})
			return s
		},
		"plugin_share_buttons_style": func() string { s, _ := style(nil); return s },
		"plugin_share_buttons_page_url": func(p string) string {
			raw, _ := json.Marshal(p)
			s, _ := pageURL([]json.RawMessage{raw})
			return s
		},
	}
}

// failingFuncs model a host-level failure: every func returns "".
func failingFuncs(t *testing.T) template.FuncMap {
	return stubFuncs(readManifest(t))
}

func renderWith(t *testing.T, funcs template.FuncMap, name string, data any) string {
	t.Helper()
	tpl, err := template.New("t").Funcs(funcs).Parse(readFile(t, partialFile) + readFile(t, sectionFile))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, name, data); err != nil {
		t.Fatalf("execute %s: %v", name, err)
	}
	return b.String()
}

func partialDot(canonical, title, name string) pageDot {
	var d pageDot
	d.Head.Canonical = canonical
	d.Page.Name = name
	if title != "" {
		d.Entity = &entity{Title: title}
	}
	return d
}

func sectionWith(title, path string, content map[string]any) sectionDot {
	d := sectionDot{Title: title, Content: content}
	d.Page.Path = path
	return d
}

type link struct {
	Net   string
	Class string
	Attrs map[string]string
	Href  string
}

var allowedElements = map[string]bool{"nav": true, "ul": true, "li": true, "a": true, "span": true}

var bannedElements = strings.Fields(`script style iframe object embed link meta base form svg math use animate set
foreignobject noscript template textarea title xmp plaintext noembed noframes frame frameset applet portal dialog`)

var bannedAttrs = map[string]bool{"srcdoc": true, "formaction": true, "http-equiv": true, "xlink:href": true, "action": true}

// scan tokenizes out and returns the links plus every banned construct found
// (a mirror of the rules core applies to plugin markup).
func scan(out string) (links []link, problems []string) {
	z := html.NewTokenizer(strings.NewReader(out))
	var cur *link
	for {
		tt := z.Next()
		if tt == html.ErrorToken {
			return
		}
		switch tt {
		case html.StartTagToken, html.SelfClosingTagToken:
			name, hasAttr := z.TagName()
			el := string(name)
			for _, b := range bannedElements {
				if el == b {
					problems = append(problems, "element "+el)
				}
			}
			if !allowedElements[el] {
				problems = append(problems, "unexpected element "+el)
			}
			attrs := map[string]string{}
			for hasAttr {
				var k, v []byte
				k, v, hasAttr = z.TagAttr()
				key, val := string(k), string(v)
				attrs[key] = val
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
			}
			if strings.Contains(attrs["href"], "#ZgotmplZ") {
				problems = append(problems, "filtered href")
			}
			if el == "li" {
				cur = &link{Class: attrs["class"]}
				cur.Net = strings.TrimPrefix(strings.Fields(attrs["class"])[1], "share-buttons__item--")
			}
			if el == "a" && cur != nil {
				cur.Attrs, cur.Href = attrs, attrs["href"]
				links = append(links, *cur)
			}
		}
	}
}

func scanOK(t *testing.T, out string) []link {
	t.Helper()
	links, problems := scan(out)
	if len(problems) > 0 {
		t.Fatalf("banned markup %v in %s", problems, out)
	}
	return links
}

func nets(links []link) []string {
	var out []string
	for _, l := range links {
		out = append(out, l.Net)
	}
	return out
}

func collapse(s string) string { return strings.Join(strings.Fields(s), " ") }

func TestParsesWithOnlyManifestFuncs(t *testing.T) {
	m := readManifest(t)
	for file, want := range map[string]string{partialFile: partialName, sectionFile: sectionName} {
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
	if m.Name != "share-buttons" || m.Version != "0.1.0" || m.SDK != "^0.8" {
		t.Errorf("name/version/sdk = %q %q %q", m.Name, m.Version, m.SDK)
	}
	if !reflect.DeepEqual(m.Permissions, []string{"settings"}) {
		t.Errorf("permissions = %v", m.Permissions)
	}
	if !reflect.DeepEqual(m.Funcs, []string{"network", "style", "page_url"}) {
		t.Errorf("funcs = %v", m.Funcs)
	}
	if m.Limits["memory_pages"] == 0 || m.Limits["max_instances"] == 0 {
		t.Errorf("limits = %v", m.Limits)
	}
	var props map[string]json.RawMessage
	if err := json.Unmarshal(m.SettingsSchema["properties"], &props); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"networks", "style", "site_url"} {
		if _, ok := props[k]; !ok {
			t.Errorf("settings_schema lacks %s", k)
		}
	}
	if len(props) != 3 {
		t.Errorf("settings_schema has %d properties, want 3", len(props))
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal([]byte(readFile(t, "plugin.json")), &raw); err != nil {
		t.Fatal(err)
	}
	for _, k := range []string{"hooks", "allowed_hosts", "site"} {
		if _, ok := raw[k]; ok {
			t.Errorf("manifest has %q", k)
		}
	}
	if strings.Contains(readFile(t, "plugin.json"), "writeOnly") {
		t.Error("no setting is secret")
	}
}

func TestRenderDefaults(t *testing.T) {
	out := renderWith(t, realFuncs(t, `{}`), partialName, partialDot("https://example.com/a", "A post", "Home"))
	links := scanOK(t, out)
	if want := defaultNetworks; !reflect.DeepEqual(nets(links), want) {
		t.Fatalf("order = %v, want %v", nets(links), want)
	}
	if !strings.Contains(out, `class="share-buttons share-buttons--both"`) {
		t.Errorf("style class missing: %s", out)
	}
	for _, l := range links {
		if l.Attrs["aria-label"] == "" {
			t.Errorf("%s: no aria-label", l.Net)
		}
		if l.Net == "email" {
			if !strings.HasPrefix(l.Href, "mailto:") || l.Attrs["target"] != "" {
				t.Errorf("email link = %v", l.Attrs)
			}
			continue
		}
		if l.Attrs["target"] != "_blank" || l.Attrs["rel"] != "noopener noreferrer" {
			t.Errorf("%s: target/rel = %q %q", l.Net, l.Attrs["target"], l.Attrs["rel"])
		}
	}
}

func TestOrderSubsetAndStyle(t *testing.T) {
	for fixture, c := range map[string]struct {
		want  []string
		class string
	}{
		`{"networks":["email","bluesky","x"],"style":"icons"}`:                  {[]string{"email", "bluesky", "x"}, "share-buttons--icons"},
		`{"networks":["bluesky"],"style":"labels"}`:                             {[]string{"bluesky"}, "share-buttons--labels"},
		`{"networks":["x","x","bogus"],"style":"weird"}`:                        {[]string{"x"}, "share-buttons--both"},
		`{"networks":["linkedin","x","facebook","whatsapp","bluesky","email"]}`: {allNetworks, "share-buttons--both"},
	} {
		for name, out := range map[string]string{
			"partial": renderWith(t, realFuncs(t, fixture), partialName, partialDot("https://example.com/a", "T", "")),
			"section": renderWith(t, realFuncs(t, fixture), sectionName, sectionWith("T", "", map[string]any{"url": "https://example.com/a"})),
		} {
			if got := nets(scanOK(t, out)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s: order = %v, want %v", name, fixture, got, c.want)
			}
			if !strings.Contains(out, `class="share-buttons `+c.class+`"`) {
				t.Errorf("%s %s: class %s missing", name, fixture, c.class)
			}
		}
	}
}

func TestFuncFailureStillRendersDefaults(t *testing.T) {
	out := renderWith(t, failingFuncs(t), partialName, partialDot("https://example.com/a", "T", ""))
	if got := nets(scanOK(t, out)); !reflect.DeepEqual(got, defaultNetworks) {
		t.Fatalf("order = %v, want %v", got, defaultNetworks)
	}
	if !strings.Contains(out, "share-buttons--both") {
		t.Errorf("style not defaulted: %s", out)
	}
	out = renderWith(t, failingFuncs(t), sectionName, sectionWith("T", "", map[string]any{"url": "https://example.com/a"}))
	if got := nets(scanOK(t, out)); !reflect.DeepEqual(got, defaultNetworks) {
		t.Fatalf("section order = %v", got)
	}
}

func TestNothingWithoutURL(t *testing.T) {
	f := realFuncs(t, `{}`)
	if out := renderWith(t, f, partialName, partialDot("", "T", "N")); strings.TrimSpace(out) != "" {
		t.Errorf("partial without canonical: %q", out)
	}
	if out := renderWith(t, f, sectionName, sectionWith("T", "/p", map[string]any{})); strings.TrimSpace(out) != "" {
		t.Errorf("section without site_url or url: %q", out)
	}
	f = realFuncs(t, `{"site_url":"not a url"}`)
	if out := renderWith(t, f, sectionName, sectionWith("T", "/p", nil)); strings.TrimSpace(out) != "" {
		t.Errorf("section with an invalid site_url: %q", out)
	}
	f = realFuncs(t, `{"site_url":"https://example.com"}`)
	if out := renderWith(t, f, sectionName, sectionWith("T", "p", nil)); strings.TrimSpace(out) != "" {
		t.Errorf("section with a relative path: %q", out)
	}
}

// param decodes one query value of a link.
func param(t *testing.T, l link, key string) string {
	t.Helper()
	if rest, ok := strings.CutPrefix(l.Href, "mailto:?"); ok {
		q, err := url.ParseQuery(rest)
		if err != nil {
			t.Fatal(err)
		}
		return q.Get(key)
	}
	u, err := url.Parse(l.Href)
	if err != nil {
		t.Fatal(err)
	}
	return u.Query().Get(key)
}

func TestTitleAndURLSources(t *testing.T) {
	f := realFuncs(t, `{"site_url":"https://site.example/","networks":["x"]}`)
	title := func(out string) string { return param(t, scanOK(t, out)[0], "text") }

	if got := title(renderWith(t, f, partialName, partialDot("https://e.com/a", "Entity", "Name"))); got != "Entity" {
		t.Errorf("partial title = %q", got)
	}
	if got := title(renderWith(t, f, partialName, partialDot("https://e.com/a", "", "Name"))); got != "Name" {
		t.Errorf("partial title fallback = %q", got)
	}
	l := scanOK(t, renderWith(t, f, sectionName, sectionWith("Sec", "/blog/x", map[string]any{})))[0]
	if param(t, l, "url") != "https://site.example/blog/x" || param(t, l, "text") != "Sec" {
		t.Errorf("section from path = %v", l.Href)
	}
	l = scanOK(t, renderWith(t, f, sectionName, sectionWith("Sec", "/blog/x", map[string]any{"url": "https://own.example/z", "title": "Own"})))[0]
	if param(t, l, "url") != "https://own.example/z" || param(t, l, "text") != "Own" {
		t.Errorf("section content override = %v", l.Href)
	}
}

func TestHostileInput(t *testing.T) {
	titles := []string{`"><script>alert(1)</script>`, `a&b=c#d`, `x" onmouseover="alert(1)`, "line\u2028<", `plain`, `100% & more+less`}
	urls := []string{`javascript:alert(1)`, `https://e.com/?a=1&b=2"`, `https://e.com/a b?q=<x>#frag`, `https://e.com/`}
	hosts := map[string]string{
		"linkedin": "www.linkedin.com", "x": "x.com", "facebook": "www.facebook.com",
		"whatsapp": "wa.me", "bluesky": "bsky.app",
	}
	f := realFuncs(t, `{"networks":["linkedin","x","facebook","whatsapp","bluesky","email"]}`)
	for _, ti := range titles {
		for _, u := range urls {
			for name, out := range map[string]string{
				"partial": renderWith(t, f, partialName, partialDot(u, ti, "")),
				"section": renderWith(t, f, sectionName, sectionWith("", "", map[string]any{"url": u, "title": ti})),
			} {
				if name == "partial" && strings.HasPrefix(u, "javascript:") {
					if strings.TrimSpace(out) != "" {
						t.Fatalf("partial rendered a non-http canonical: %s", out)
					}
					continue
				}
				links := scanOK(t, out)
				if len(links) != 6 {
					t.Fatalf("%s: %d links", name, len(links))
				}
				for _, l := range links {
					pu, err := url.Parse(l.Href)
					if err != nil {
						t.Fatal(err)
					}
					wantText := ti
					switch l.Net {
					case "email":
						if pu.Scheme != "mailto" || param(t, l, "subject") != ti || param(t, l, "body") != u {
							t.Errorf("%s email: subject/body = %q %q", name, param(t, l, "subject"), param(t, l, "body"))
						}
						continue
					case "linkedin", "x":
						if got := param(t, l, "url"); got != u {
							t.Errorf("%s %s: url = %q, want %q", name, l.Net, got, u)
						}
					case "facebook":
						if got := param(t, l, "u"); got != u {
							t.Errorf("facebook: u = %q, want %q", got, u)
						}
					case "whatsapp", "bluesky":
						wantText = ti + " " + u
					}
					if pu.Scheme != "https" || pu.Host != hosts[l.Net] {
						t.Errorf("%s %s: href = %q", name, l.Net, l.Href)
					}
					if l.Net != "linkedin" && l.Net != "facebook" {
						if got := param(t, l, "text"); got != wantText {
							t.Errorf("%s %s: text = %q, want %q", name, l.Net, got, wantText)
						}
					}
				}
			}
		}
	}
}

func TestBannedMarkupScanner(t *testing.T) {
	for _, bad := range []string{
		`<svg></svg>`, `<script>1</script>`, `<style>a{}</style>`, `<a onclick="x">`, `<a href="JaVa script:alert(1)">`,
		`<a href=" data:text/html,x">`, `<a href="vbscript:x">`, `<form action="/x">`, `<iframe srcdoc="x">`,
	} {
		if _, problems := scan(bad); len(problems) == 0 {
			t.Errorf("scanner accepted %s", bad)
		}
	}
}

func TestPartialSectionParity(t *testing.T) {
	for _, fixture := range []string{`{}`, `{"networks":["bluesky","email","x"],"style":"labels"}`} {
		f := realFuncs(t, fixture)
		p := renderWith(t, f, partialName, partialDot("https://example.com/a?b=1", "Title & more", ""))
		s := renderWith(t, f, sectionName, sectionWith("", "", map[string]any{"url": "https://example.com/a?b=1", "title": "Title & more"}))
		if collapse(p) != collapse(s) {
			t.Errorf("%s: partial and section differ:\n%s\n%s", fixture, p, s)
		}
	}
}

// The stylesheet is linked on every public page, so every class must carry the
// plugin prefix to stay clear of theme classes.
func TestClassNamesArePrefixed(t *testing.T) {
	const prefix = "share-buttons"
	ok := func(c string) bool {
		return c == prefix || strings.HasPrefix(c, prefix+"__") || strings.HasPrefix(c, prefix+"--")
	}
	actions := regexp.MustCompile(`\{\{.*?\}\}`)
	attr := regexp.MustCompile(`class="([^"]*)"`)
	seen := 0
	for _, f := range []string{partialFile, sectionFile} {
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
	css := readFile(t, "assets/plugin.css")
	for _, rule := range strings.Split(css, "}") {
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
	if seen < 20 {
		t.Errorf("only %d class tokens found: the check is not looking", seen)
	}
}

// funcsWith serves network answers from nets (index -> answer, "" for a
// failure) and counts the network calls.
func funcsWith(t *testing.T, nets func(i int) string, calls *int) template.FuncMap {
	f := stubFuncs(readManifest(t))
	f["plugin_share_buttons_network"] = func(i int) string { *calls++; return nets(i) }
	return f
}

// The fallback is decided once per render: a failure of the first call gives
// the pure default list, a later failure ends the list, and the two are never
// mixed.
func TestFallbackIsNotMixed(t *testing.T) {
	cfg := []string{"bluesky", "x"}
	cases := map[string]struct {
		answer func(i int) string
		want   []string
		calls  int
	}{
		"first call fails": {func(i int) string { return "" }, defaultNetworks, 1},
		"later call fails": {func(i int) string {
			if i == 0 {
				return "bluesky"
			}
			return ""
		}, []string{"bluesky"}, 2},
		"configured": {func(i int) string {
			if i < len(cfg) {
				return cfg[i]
			}
			return "-"
		}, cfg, 3},
		"first answer ok, second fails, third would succeed": {func(i int) string {
			return []string{"bluesky", "", "x"}[i]
		}, []string{"bluesky"}, 2},
	}
	for name, c := range cases {
		for tpl, data := range map[string]any{
			partialName: partialDot("https://example.com/a", "T", ""),
			sectionName: sectionWith("T", "", map[string]any{"url": "https://example.com/a"}),
		} {
			calls := 0
			out := renderWith(t, funcsWith(t, c.answer, &calls), tpl, data)
			if got := nets(scanOK(t, out)); !reflect.DeepEqual(got, c.want) {
				t.Errorf("%s %s: networks = %v, want %v", name, tpl, got, c.want)
			}
			if calls > c.calls {
				t.Errorf("%s %s: %d network calls, want at most %d", name, tpl, calls, c.calls)
			}
		}
	}
}

// A site without a public base URL has a bare path as canonical address: a
// relative share link is useless, so the partial renders nothing.
func TestPartialNeedsAbsoluteCanonical(t *testing.T) {
	f := realFuncs(t, `{}`)
	for _, c := range []string{"/blog/x", "blog/x", "//host/x", "ftp://h/x", "http:/", "https:/", "h", "https://"[:7]} {
		if out := renderWith(t, f, partialName, partialDot(c, "T", "N")); strings.TrimSpace(out) != "" {
			t.Errorf("canonical %q rendered %q", c, out)
		}
	}
	for _, c := range []string{"https://example.com/blog/x", "http://example.com/x", "http://a", "https://a"} {
		out := renderWith(t, f, partialName, partialDot(c, "T", "N"))
		links := scanOK(t, out)
		if len(links) == 0 || param(t, links[0], "url") != c {
			t.Errorf("canonical %q rendered %q", c, out)
		}
	}
}
