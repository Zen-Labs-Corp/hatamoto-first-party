package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"testing"
	texttemplate "text/template"
	"text/template/parse"

	"golang.org/x/net/html"
)

// This file mirrors, at source level, the structural rule core applies to a
// plugin's partials and sections (the rendered output is scanned separately in
// templates_test.go):
//
//   - a printing action may stand in text (outside raw-text elements) or
//     entirely inside a quoted attribute value;
//   - if/range/with/break/continue and {{template}} may stand in text only;
//   - every branch starts and ends in text and is judged on its own.
//
// Each branch gets an inert token in place of every action and is run through
// the HTML tokenizer.

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
	if strings.Contains(strings.ToLower(src), placeholderPrefix) {
		return fmt.Errorf("source contains the placeholder prefix")
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

// branch judges one list, then each body nested in it on its own.
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

// structureFuncs declares only the manifest funcs, as core does.
func structureFuncs(t *testing.T) texttemplate.FuncMap {
	t.Helper()
	fm := texttemplate.FuncMap{}
	for name := range stubFuncs(readManifest(t)) {
		fm[name] = func(...any) string { return "" }
	}
	return fm
}

func TestStructureRealTemplates(t *testing.T) {
	for _, file := range []string{buttonFile, buttonSiteFile, sectionFile} {
		if err := checkStructure(file, readFile(t, file), structureFuncs(t)); err != nil {
			t.Errorf("%s: %v", file, err)
		}
	}
}

// The section holds the plugin's one iframe (allowed because site.scripts is
// granted). Everything else dangerous stays banned in the source.
func TestSectionSourceMarkup(t *testing.T) {
	src := strings.ToLower(readFile(t, sectionFile))
	if n := strings.Count(src, "<iframe"); n != 1 {
		t.Fatalf("section has %d iframes, want 1", n)
	}
	i := strings.Index(src, "<iframe")
	tag := src[i : i+strings.Index(src[i:], ">")]
	for _, want := range []string{"sandbox=", `loading="lazy"`, "title=", "referrerpolicy="} {
		if !strings.Contains(tag, want) {
			t.Errorf("iframe tag lacks %s", want)
		}
	}
	for _, file := range []string{buttonFile, buttonSiteFile, sectionFile} {
		low := strings.ToLower(readFile(t, file))
		for _, bad := range []string{"<script", "srcdoc", "javascript:", "data:", "vbscript:", "<style", "<object", "<embed", "<form", "<svg"} {
			if strings.Contains(low, bad) {
				t.Errorf("%s contains %s", file, bad)
			}
		}
		if regexp.MustCompile(`\son[a-z]+\s*=`).MatchString(low) {
			t.Errorf("%s has an on* attribute", file)
		}
	}
}

func TestStructureRule(t *testing.T) {
	funcs := texttemplate.FuncMap{}
	bad := []string{
		`<a {{if .X}}hidden{{end}}>`,
		`<scr{{.X}}ipt>`,
		`<p {{.A}}>`,
		`<a href={{.X}}>`,
		`<a class="a {{if .X}}b{{end}}">`,
		`<style>a{color:{{.X}}}</style>`,
		`<a href="{{template "x" .}}">`,
		`{{if .X}}<a href="x{{end}}`,
		`<!-- {{.X}} -->`,
		`</a{{.X}}>`,
	}
	for _, src := range bad {
		if err := checkStructure("bad", src, funcs); err == nil {
			t.Errorf("accepted: %s", src)
		}
	}
	good := []string{
		`<a href="u?x={{.X}}">{{.Y}}</a>`,
		`<a href="u?x={{.X}}&y={{.Y}}" class="{{.C}}">t</a>`,
		`{{range $i := 3}}<li>{{$i}}</li>{{end}}`,
		`{{$n := "a"}}{{if .X}}{{$n = "x"}}{{end}}`,
		// Core only asks that a branch starts and ends in text: a branch that
		// opens an element and leaves it open is allowed.
		`{{if .X}}<a href="x">{{end}}`,
	}
	for _, src := range good {
		if err := checkStructure("good", src, funcs); err != nil {
			t.Errorf("rejected %s: %v", src, err)
		}
	}
}
