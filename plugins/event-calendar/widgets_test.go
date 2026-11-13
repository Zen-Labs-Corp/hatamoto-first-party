package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
)

const (
	widgetMax       = 16 * 1024
	widgetIslandMax = 32768
)

var (
	widgetNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)
	sourceRe     = regexp.MustCompile(`^ext\.[a-z][a-z0-9-]{1,39}\.[a-z][a-z0-9_-]{0,31}$`)
	actionRe     = regexp.MustCompile(`(?s)\{\{(.*?)\}\}`)
	stringLitRe  = regexp.MustCompile(`"[^"]*"`)
	funcWordRe   = regexp.MustCompile(`(?:^|[\s(|])([a-z][a-z_]*)\b`)
	bannedTagRe  = regexp.MustCompile(`(?i)<\s*(script|style|iframe|object|embed|link|meta|base|form|svg|math|main|header|footer)\b`)
	handlerRe    = regexp.MustCompile(`(?i)\son[a-z]+\s*=`)
	siblingRe    = regexp.MustCompile(`[+~]`)

	// islandBans mirrors the host's island lint.
	islandBans = map[string]*regexp.Regexp{
		"absolute URL":      regexp.MustCompile(`(?i)https?://`),
		"eval":              regexp.MustCompile(`\beval\s*\(`),
		"new Function":      regexp.MustCompile(`\bnew\s+Function\b`),
		"document.write":    regexp.MustCompile(`\bdocument\s*\.\s*write`),
		"dynamic import":    regexp.MustCompile(`\bimport\s*\(`),
		"importScripts":     regexp.MustCompile(`\bimportScripts\b`),
		"protocol-relative": regexp.MustCompile("[\"'`]//[A-Za-z0-9]"),
	}

	// widgetFiles are the only files the host accepts in a widget directory.
	widgetFiles   = []string{"schema.json", "widget.html", "widget.css", "island.js"}
	templateFuncs = []string{"if", "else", "end", "range", "with", "eq", "ne", "lt", "le", "gt", "ge", "and", "or", "not", "len", "index"}
)

func widgetDirs(t *testing.T) []string {
	t.Helper()
	entries, err := os.ReadDir("widgets")
	if err != nil {
		t.Fatal(err)
	}
	var dirs []string
	for _, e := range entries {
		if !e.IsDir() {
			t.Errorf("widgets/%s is a file", e.Name())
			continue
		}
		dirs = append(dirs, e.Name())
	}
	return dirs
}

func readWidget(t *testing.T, name, file string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("widgets", name, file))
	if err != nil {
		t.Fatal(err)
	}
	limit := widgetMax
	if file == "island.js" {
		limit = widgetIslandMax
	}
	if len(b) > limit {
		t.Errorf("%s/%s is %d bytes, max %d", name, file, len(b), limit)
	}
	return string(b)
}

func TestWidgetsValid(t *testing.T) {
	dirs := widgetDirs(t)
	slices.Sort(dirs)
	if want := []string{"calendar", "countdown", "event-card", "event-details", "map", "mini-calendar", "venue-card"}; !slices.Equal(dirs, want) {
		t.Fatalf("widgets = %v, want %v", dirs, want)
	}
	if len(dirs) > 16 {
		t.Fatalf("%d widgets, a package may ship 16", len(dirs))
	}
	for _, name := range dirs {
		t.Run(name, func(t *testing.T) {
			if !widgetNameRe.MatchString(name) {
				t.Errorf("name %q", name)
			}
			files, _ := os.ReadDir(filepath.Join("widgets", name))
			for _, f := range files {
				if !slices.Contains(widgetFiles, f.Name()) {
					t.Errorf("unexpected file %s", f.Name())
				}
			}
			checkSchema(t, name)
			checkTemplate(t, name)
			checkCSS(t, name)
			checkIsland(t, name)
		})
	}
}

// checkIsland applies the host's island lint to island.js when the widget has one.
func checkIsland(t *testing.T, name string) {
	if _, err := os.Stat(filepath.Join("widgets", name, "island.js")); err != nil {
		return
	}
	src := readWidget(t, name, "island.js")
	if _, err := os.Stat(filepath.Join("jstest", name+"_test.js")); err != nil {
		t.Errorf("island.js has no jstest/%s_test.js", name)
	}
	for _, host := range []string{"calendar.google.com", "outlook.live.com", "openstreetmap.org"} {
		if strings.Contains(src, host) {
			t.Errorf("island.js carries the literal %s; provider addresses come from data- attributes", host)
		}
	}
	for what, re := range islandBans {
		if re.MatchString(src) {
			t.Errorf("island.js contains %s: %s", what, re.FindString(src))
		}
	}
}

func checkSchema(t *testing.T, name string) {
	var sc struct {
		AdditionalProperties *bool                      `json:"additionalProperties"`
		XHM                  map[string]json.RawMessage `json:"x-hm"`
		Properties           map[string]map[string]any  `json:"properties"`
	}
	if err := json.Unmarshal([]byte(readWidget(t, name, "schema.json")), &sc); err != nil {
		t.Fatal(err)
	}
	if sc.AdditionalProperties == nil || *sc.AdditionalProperties {
		t.Error("additionalProperties must be false")
	}
	if _, ok := sc.XHM["label"]; !ok {
		t.Error("x-hm.label missing")
	}
	if _, ok := sc.XHM["group"]; ok {
		t.Error("x-hm.group is not allowed")
	}
	if len(sc.Properties) > 24 {
		t.Errorf("%d props", len(sc.Properties))
	}
	for p, def := range sc.Properties {
		if def["type"] == "array" {
			t.Errorf("prop %s is an array", p)
		}
		if b, ok := def["x-hm-bind"]; ok && b != "text" && b != "url" && b != "image" {
			t.Errorf("prop %s binds %v", p, b)
		}
	}
}

func checkTemplate(t *testing.T, name string) {
	src := readWidget(t, name, "widget.html")
	root, ok := strings.CutPrefix(strings.TrimSpace(src), "<")
	if !ok || strings.Count(src, "{{.Attrs}}") != 1 || !strings.Contains(root[:strings.Index(root, ">")], "{{.Attrs}}") {
		t.Error("the root element must carry {{.Attrs}}")
	}
	if bannedTagRe.MatchString(src) {
		t.Errorf("banned element: %s", bannedTagRe.FindString(src))
	}
	if handlerRe.MatchString(src) {
		t.Error("event handler attribute")
	}
	low := strings.ToLower(src)
	for _, bad := range []string{"style=", "javascript:", "data:", "webcal:", "srcdoc", "<!", "<?"} {
		if strings.Contains(strings.ReplaceAll(low, "data-", ""), bad) {
			t.Errorf("contains %q", bad)
		}
	}
	opens := 0
	for _, m := range actionRe.FindAllStringSubmatch(src, -1) {
		act := strings.TrimSpace(m[1])
		for _, w := range funcWordRe.FindAllStringSubmatch(stringLitRe.ReplaceAllString(act, ""), -1) {
			if !slices.Contains(templateFuncs, w[1]) {
				t.Errorf("{{%s}}: %q is not an allowed function", act, w[1])
			}
		}
		switch strings.Fields(act + " x")[0] {
		case "if", "range", "with":
			opens++
		}
		if strings.HasPrefix(act, "range") && !strings.HasPrefix(act, "range .P") {
			t.Errorf("range over %q", act)
		}
	}
	if ends := strings.Count(src, "{{end}}"); opens != ends {
		t.Errorf("%d openers, %d {{end}}", opens, ends)
	}
}

// checkCSS requires every selector to start with the widget's root class on a
// class boundary and refuses the constructs a pack may not use.
func checkCSS(t *testing.T, name string) {
	css := readWidget(t, name, "widget.css")
	for _, bad := range []string{"url(", ":has(", "@import", "@font-face", "@keyframes", "expression", "absolute", "fixed", "sticky"} {
		if strings.Contains(css, bad) {
			t.Errorf("contains %q", bad)
		}
	}
	if siblingRe.MatchString(stripCSSParens(css)) {
		t.Error("sibling combinators are not allowed")
	}
	root := ".hm-event-calendar_" + name
	var buf strings.Builder
	depth := 0
	for _, c := range css {
		switch c {
		case '{':
			prelude := strings.TrimSpace(buf.String())
			buf.Reset()
			if strings.HasPrefix(prelude, "@") {
				if !strings.HasPrefix(prelude, "@media") && !strings.HasPrefix(prelude, "@supports") {
					t.Errorf("at-rule %q", prelude)
				}
				continue
			}
			depth++
			for _, sel := range strings.Split(prelude, ",") {
				sel = strings.TrimSpace(sel)
				rest, ok := strings.CutPrefix(sel, root)
				if !ok || (rest != "" && strings.ContainsAny(rest[:1], "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789_-")) {
					t.Errorf("selector %q does not start with %s", sel, root)
				}
			}
		case '}':
			if depth > 0 {
				depth--
			}
			buf.Reset()
		default:
			if depth == 0 {
				buf.WriteRune(c)
			}
		}
	}
}

// stripCSSParens drops parenthesised groups (calc(), :nth-child(2n+1)) so
// that only selector combinators remain for the sibling check.
func stripCSSParens(css string) string {
	var b strings.Builder
	depth := 0
	for _, c := range css {
		switch {
		case c == '(':
			depth++
		case c == ')' && depth > 0:
			depth--
		case depth == 0:
			b.WriteRune(c)
		}
	}
	return b.String()
}

type seedNode struct {
	T string            `json:"t"`
	P map[string]any    `json:"p"`
	B map[string]string `json:"b"`
	C []seedNode        `json:"c"`
}

func TestEventDetailsSeed(t *testing.T) {
	var sc struct {
		XHM struct {
			Children bool       `json:"children"`
			Seed     []seedNode `json:"seed"`
		} `json:"x-hm"`
	}
	if err := json.Unmarshal([]byte(readWidget(t, "event-details", "schema.json")), &sc); err != nil {
		t.Fatal(err)
	}
	if !sc.XHM.Children || len(sc.XHM.Seed) != 1 {
		t.Fatalf("children=%v seed=%d", sc.XHM.Children, len(sc.XHM.Seed))
	}
	loop := sc.XHM.Seed[0]
	src, _ := loop.P["source"].(string)
	if loop.T != "loop" || loop.P["from"] != "source" || src != "ext.event-calendar.event" || !sourceRe.MatchString(src) || loop.P["limit"] != float64(1) {
		t.Fatalf("loop = %+v", loop)
	}
	if len(loop.C) != 2 || loop.C[0].T != "event-calendar_event-card" || loop.C[1].T != "loopempty" {
		t.Fatalf("loop children = %+v", loop.C)
	}

	var card struct {
		Properties map[string]struct {
			Bind string `json:"x-hm-bind"`
		} `json:"properties"`
	}
	if err := json.Unmarshal([]byte(readWidget(t, "event-card", "schema.json")), &card); err != nil {
		t.Fatal(err)
	}
	bound := loop.C[0].B
	if len(bound) != len(card.Properties) {
		t.Errorf("seed binds %d props, the card has %d", len(bound), len(card.Properties))
	}
	for p, def := range card.Properties {
		from, ok := bound[p]
		if !ok || !strings.HasPrefix(from, "loop.item.") {
			t.Errorf("prop %s is bound to %q", p, from)
		}
		if def.Bind != "text" {
			t.Errorf("prop %s binds %q; loop attributes bind to text props", p, def.Bind)
		}
	}
	// every card.P.<name> the template reads is a declared prop
	for _, m := range regexp.MustCompile(`\.P\.([a-z_]+)`).FindAllStringSubmatch(readWidget(t, "event-card", "widget.html"), -1) {
		if _, ok := card.Properties[m[1]]; !ok {
			t.Errorf("template reads undeclared prop %s", m[1])
		}
	}
	html := readWidget(t, "event-card", "widget.html")
	if !strings.Contains(html, `<span class="badge">{{.P.status}}</span>`) || !strings.Contains(html, `<p class="tickets">`) {
		t.Error("the card needs the status badge beside the title and the tickets block")
	}
	if !strings.Contains(html, `data-tiles="tile.openstreetmap.org"`) || strings.Contains(html, "https://") {
		t.Error("the map block needs the literal tile host and no absolute URL")
	}
}

// TestSeedDepth keeps every seed within the host's nesting limit of three.
func TestSeedDepth(t *testing.T) {
	var depth func(nodes []seedNode) int
	depth = func(nodes []seedNode) int {
		d := 0
		for _, n := range nodes {
			d = max(d, 1+depth(n.C))
		}
		return d
	}
	for _, name := range widgetDirs(t) {
		var sc struct {
			XHM struct {
				Children bool       `json:"children"`
				Seed     []seedNode `json:"seed"`
			} `json:"x-hm"`
		}
		if err := json.Unmarshal([]byte(readWidget(t, name, "schema.json")), &sc); err != nil {
			t.Fatal(err)
		}
		if len(sc.XHM.Seed) > 0 && !sc.XHM.Children {
			t.Errorf("%s: a seed needs x-hm.children", name)
		}
		if d := depth(sc.XHM.Seed); d > 3 {
			t.Errorf("%s: seed depth %d, max 3", name, d)
		}
	}
}

func TestCalendarSeedAndProviders(t *testing.T) {
	var sc struct {
		XHM struct {
			Seed []seedNode `json:"seed"`
		} `json:"x-hm"`
	}
	if err := json.Unmarshal([]byte(readWidget(t, "calendar", "schema.json")), &sc); err != nil {
		t.Fatal(err)
	}
	if len(sc.XHM.Seed) != 1 {
		t.Fatalf("seed = %+v", sc.XHM.Seed)
	}
	loop := sc.XHM.Seed[0]
	src, _ := loop.P["source"].(string)
	if loop.T != "loop" || loop.P["from"] != "source" || src != "ext.event-calendar.upcoming" || !sourceRe.MatchString(src) {
		t.Fatalf("loop = %+v", loop)
	}
	if len(loop.C) != 2 || loop.C[0].T != "container" || loop.C[1].T != "loopempty" {
		t.Fatalf("loop children = %+v", loop.C)
	}
	html := readWidget(t, "calendar", "widget.html")
	for _, want := range []string{`data-gcal="https://calendar.google.com/`, `data-outlook="https://outlook.live.com/`, `data-api="/p/event-calendar/api/events"`} {
		if !strings.Contains(html, want) {
			t.Errorf("calendar widget.html lacks %s", want)
		}
	}
}
