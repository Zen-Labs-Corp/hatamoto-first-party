package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"os"
	"strconv"
	"strings"
	"testing"
	texttemplate "text/template"
	"text/template/parse"

	"golang.org/x/net/html"
)

const (
	partialFile = "partials/form.html"
	partialName = "plugin/ratings/form"
)

// pageDot mirrors the field names core puts in a page's template data; a
// field missing here is an execution error, as in the real engine.
type pageDot struct {
	EntityID   string
	SchemaSlug string
	Path       string
}

func readPartial(t *testing.T) string {
	t.Helper()
	b, err := os.ReadFile(partialFile)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func renderPartial(t *testing.T, dot pageDot, enabled, summary string) string {
	t.Helper()
	funcs := template.FuncMap{
		"plugin_ratings_enabled": func(string) string { return enabled },
		"plugin_ratings_summary": func(string) string { return summary },
	}
	tpl, err := template.New("p").Funcs(funcs).Parse(readPartial(t))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, partialName, dot); err != nil {
		t.Fatal(err)
	}
	return b.String()
}

type element struct {
	tag   string
	attrs map[string]string
}

func elements(t *testing.T, src string) []element {
	t.Helper()
	var out []element
	z := html.NewTokenizer(strings.NewReader(src))
	for {
		switch z.Next() {
		case html.ErrorToken:
			return out
		case html.StartTagToken, html.SelfClosingTagToken:
			name, more := z.TagName()
			e := element{tag: string(name), attrs: map[string]string{}}
			for more {
				var k, v []byte
				k, v, more = z.TagAttr()
				e.attrs[string(k)] = string(v)
			}
			out = append(out, e)
		}
	}
}

var goodDot = pageDot{EntityID: testID, SchemaSlug: "post", Path: "/blog/a"}

func TestPartialRendersTheForm(t *testing.T) {
	out := renderPartial(t, goodDot, "1", "4.3 out of 5 (27 ratings)")
	var form *element
	hidden := map[string]string{}
	var buttons []string
	for _, e := range elements(t, out) {
		switch {
		case e.tag == "form":
			form = &e
		case e.tag == "input" && e.attrs["type"] == "hidden":
			hidden[e.attrs["name"]] = e.attrs["value"]
		case e.tag == "button":
			if e.attrs["type"] != "submit" || e.attrs["name"] != "stars" {
				t.Errorf("button %+v", e.attrs)
			}
			buttons = append(buttons, e.attrs["value"])
		}
	}
	if form == nil || form.attrs["action"] != "/p/ratings/rate" || form.attrs["method"] != "post" {
		t.Fatalf("form %+v", form)
	}
	if strings.Join(buttons, "") != "12345" {
		t.Fatalf("buttons %v", buttons)
	}
	for name, want := range map[string]string{"entity_id": testID, "schema": "post", "path": "/blog/a", "return_to": "/blog/a"} {
		if hidden[name] != want {
			t.Errorf("hidden %s = %q", name, hidden[name])
		}
	}
	if !strings.Contains(out, "4.3 out of 5 (27 ratings)") {
		t.Errorf("summary missing: %s", out)
	}
}

func TestPartialRendersNothingOrStillTheForm(t *testing.T) {
	for _, tc := range []struct {
		name       string
		dot        pageDot
		enabled    string
		summary    string
		form, line bool
	}{
		{"no entity", pageDot{SchemaSlug: "post"}, "1", "x", false, false},
		{"schema not enabled", goodDot, "", "x", false, false},
		{"summary empty", goodDot, "1", "", true, false},
		{"summary set", goodDot, "1", "x", true, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out := renderPartial(t, tc.dot, tc.enabled, tc.summary)
			if has := strings.Contains(out, "<form"); has != tc.form {
				t.Errorf("form = %v: %s", has, out)
			}
			if has := strings.Contains(out, "hm-ratings-summary"); has != tc.line {
				t.Errorf("summary line = %v: %s", has, out)
			}
			if !tc.form && strings.TrimSpace(out) != "" {
				t.Errorf("not empty: %q", out)
			}
		})
	}
}

func TestPartialRendersWithTheRealFuncsWhenTheDatabaseFails(t *testing.T) {
	(&fakeHost{settings: `{}`, db: func(hostCall, any) error { return fmt.Errorf("unavailable") }}).install(t)
	call := func(f func([]json.RawMessage) (string, error)) func(string) string {
		return func(s string) string {
			out, _ := f(rawArgs(s))
			return out
		}
	}
	funcs := template.FuncMap{"plugin_ratings_enabled": call(funcEnabled), "plugin_ratings_summary": call(funcSummary)}
	tpl, err := template.New("p").Funcs(funcs).Parse(readPartial(t))
	if err != nil {
		t.Fatal(err)
	}
	var b bytes.Buffer
	if err := tpl.ExecuteTemplate(&b, partialName, goodDot); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(b.String(), "<form") || strings.Contains(b.String(), "hm-ratings-summary") {
		t.Fatalf("output %s", b.String())
	}
}

func TestPartialEscapesThePath(t *testing.T) {
	dot := goodDot
	dot.Path = `/a"><script>alert(1)</script>`
	out := renderPartial(t, dot, "1", "")
	if strings.Contains(out, "<script") || strings.Contains(out, `"><`) {
		t.Fatalf("not escaped: %s", out)
	}
}

func TestPartialRenderedOutputIsInert(t *testing.T) {
	out := renderPartial(t, goodDot, "1", "4.3 out of 5 (27 ratings)")
	for _, e := range elements(t, out) {
		if e.tag == "script" || e.tag == "textarea" {
			t.Errorf("element %s", e.tag)
		}
		for k := range e.attrs {
			if k == "formaction" || strings.HasPrefix(k, "on") {
				t.Errorf("attribute %s on <%s>", k, e.tag)
			}
		}
	}
}

// The rest mirrors, at source level, the structural rule core applies to a
// plugin partial: a printing action may stand in text or entirely inside a
// quoted attribute value; if/range/with and {{template}} in text only; every
// branch starts and ends in text. Each branch gets an inert token in place of
// every action and runs through the HTML tokenizer.

type actKind int

const (
	actPrint actKind = iota
	actControl
	actTemplate
)

var actNames = [...]string{"an action", "a control action", "a template call"}

const placeholderPrefix = "hmact"

type structureCheck struct{ kinds []actKind }

func checkStructure(name, src string, funcs texttemplate.FuncMap) error {
	root, err := texttemplate.New(name).Funcs(funcs).Parse(src)
	if err != nil {
		return err
	}
	for _, tpl := range root.Templates() {
		if tpl.Tree == nil || tpl.Tree.Root == nil {
			continue
		}
		if err := (&structureCheck{}).branch(tpl.Tree.Root); err != nil {
			return fmt.Errorf("%s: %w", tpl.Name(), err)
		}
	}
	return nil
}

func (c *structureCheck) placeholder(k actKind) string {
	c.kinds = append(c.kinds, k)
	return placeholderPrefix + fmt.Sprintf("%04d", len(c.kinds)-1) + "x"
}

func (c *structureCheck) branch(l *parse.ListNode) error {
	if l == nil {
		return nil
	}
	c.kinds = c.kinds[:0]
	var b strings.Builder
	var bodies []*parse.ListNode
	for _, n := range l.Nodes {
		switch x := n.(type) {
		case *parse.TextNode:
			b.Write(x.Text)
		case *parse.ActionNode:
			b.WriteString(c.placeholder(actPrint))
		case *parse.TemplateNode:
			b.WriteString(c.placeholder(actTemplate))
		case *parse.IfNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.RangeNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.WithNode:
			b.WriteString(c.placeholder(actControl))
			bodies = append(bodies, x.List, x.ElseList)
		case *parse.BreakNode, *parse.ContinueNode:
			b.WriteString(c.placeholder(actControl))
		}
	}
	if err := c.tokens(b.String()); err != nil {
		return err
	}
	for _, body := range bodies {
		if err := c.branch(body); err != nil {
			return err
		}
	}
	return nil
}

var rawTextTags = map[string]bool{
	"script": true, "style": true, "textarea": true, "title": true, "xmp": true,
	"iframe": true, "noembed": true, "noframes": true, "noscript": true, "plaintext": true,
}

func (c *structureCheck) tokens(src string) error {
	z := html.NewTokenizer(strings.NewReader(src))
	raw := false
	for {
		tt := z.Next()
		tok := string(z.Raw())
		switch tt {
		case html.ErrorToken:
			switch {
			case tok != "":
				return fmt.Errorf("a branch ends inside a tag")
			case raw:
				return fmt.Errorf("a branch ends inside a raw-text element")
			}
			return nil
		case html.TextToken:
			if raw {
				if err := c.refuseAny(tok, "inside a raw-text element"); err != nil {
					return err
				}
			}
		case html.StartTagToken, html.SelfClosingTagToken:
			name, _ := z.TagName()
			if string(name) == "script" || string(name) == "textarea" {
				return fmt.Errorf("a <%s> element", name)
			}
			raw = rawTextTags[string(name)]
			if err := c.tag(tok); err != nil {
				return err
			}
		case html.EndTagToken:
			raw = false
			if err := c.refuseAny(tok, "inside an end tag"); err != nil {
				return err
			}
		case html.CommentToken, html.DoctypeToken:
			if !strings.HasSuffix(tok, ">") {
				return fmt.Errorf("a branch ends inside a comment")
			}
			if err := c.refuseAny(tok, "inside a comment or doctype"); err != nil {
				return err
			}
		}
	}
}

func (c *structureCheck) refuseAny(tok, where string) error {
	if occ := c.found(tok); len(occ) > 0 {
		return fmt.Errorf("%s %s", actNames[occ[0].kind], where)
	}
	return nil
}

// tag judges a start tag: only a printing action lying entirely between the
// quotes of a quoted attribute value may be in it.
func (c *structureCheck) tag(tok string) error {
	spans := quotedValues(tok)
	for _, o := range c.found(tok) {
		if o.kind != actPrint {
			return fmt.Errorf("%s inside a tag", actNames[o.kind])
		}
		in := false
		for _, sp := range spans {
			in = in || (o.pos >= sp[0] && o.end <= sp[1])
		}
		if !in {
			return fmt.Errorf("an action inside a tag name, an attribute name or an unquoted value")
		}
	}
	return nil
}

func isWS(c byte) bool {
	return c == ' ' || c == '\n' || c == '\r' || c == '\t' || c == '\f'
}

// quotedValues returns the [start, end) byte ranges of the quoted attribute
// values of a complete start tag token.
func quotedValues(tok string) [][2]int {
	var out [][2]int
	i, n := 1, len(tok)
	skip := func() {
		for i < n && isWS(tok[i]) {
			i++
		}
	}
	for i < n && !isWS(tok[i]) && tok[i] != '/' && tok[i] != '>' {
		i++
	}
	skip()
	for i < n && tok[i] != '>' {
		start := i
		for i < n {
			c := tok[i]
			if c == '=' && i == start {
				i++
				continue
			}
			if c == '=' || isWS(c) || c == '/' || c == '>' {
				break
			}
			i++
		}
		skip()
		switch {
		case i < n && tok[i] == '/':
			i++
		case i < n && tok[i] == '=':
			i++
			skip()
			switch {
			case i >= n || tok[i] == '>':
			case tok[i] == '"' || tok[i] == '\'':
				q := tok[i]
				j := strings.IndexByte(tok[i+1:], q)
				if j < 0 {
					return out
				}
				out = append(out, [2]int{i + 1, i + 1 + j})
				i += j + 2
			default:
				for i < n && !isWS(tok[i]) && tok[i] != '>' {
					i++
				}
			}
		}
		skip()
	}
	return out
}

type occurrence struct {
	pos, end int
	kind     actKind
}

// found lists the placeholders in s, in order.
func (c *structureCheck) found(s string) []occurrence {
	var out []occurrence
	for off := 0; ; {
		i := strings.Index(s[off:], placeholderPrefix)
		if i < 0 {
			return out
		}
		start := off + i
		d := start + len(placeholderPrefix)
		j := strings.IndexByte(s[d:], 'x')
		if j < 0 {
			return out
		}
		if k, err := strconv.Atoi(s[d : d+j]); err == nil && k < len(c.kinds) {
			out = append(out, occurrence{start, d + j + 1, c.kinds[k]})
		}
		off = d + j + 1
	}
}

func TestPartialStructure(t *testing.T) {
	fm := texttemplate.FuncMap{}
	for _, name := range loadManifest(t).Funcs {
		fm["plugin_ratings_"+name] = func(...any) string { return "" }
	}
	if err := checkStructure(partialFile, readPartial(t), fm); err != nil {
		t.Fatal(err)
	}
	src := readPartial(t)
	for _, bad := range []string{"<script", "formaction", "<textarea", "<iframe"} {
		if strings.Contains(strings.ToLower(src), bad) {
			t.Errorf("partial contains %q", bad)
		}
	}
}

func TestStructureRuleRejectsWhatCoreRejects(t *testing.T) {
	funcs := texttemplate.FuncMap{}
	for _, src := range []string{
		`<a {{if .X}}hidden{{end}}>`,
		`<p {{.A}}>`,
		`<a href={{.X}}>`,
		`<a class="a {{if .X}}b{{end}}">`,
		`<script>{{.X}}</script>`,
		`<a href="{{template "x" .}}">`,
		`{{if .X}}<a href="x{{end}}`,
	} {
		if err := checkStructure("bad", src, funcs); err == nil {
			t.Errorf("accepted: %s", src)
		}
	}
	for _, src := range []string{
		`<input value="{{.X}}">{{if .Y}}<p>{{.Y}}</p>{{end}}`,
		`{{if .X}}{{if .Y}}<b>t</b>{{end}}{{end}}`,
	} {
		if err := checkStructure("good", src, funcs); err != nil {
			t.Errorf("rejected %s: %v", src, err)
		}
	}
}
