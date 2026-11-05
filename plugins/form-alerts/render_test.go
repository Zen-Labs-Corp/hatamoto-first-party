package main

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"
)

// injections are values that must stay inert on every platform.
var injections = []string{"@channel", "<!here>", "<users/all>", "<b>x</b>", "[x](http://evil)", "<http://evil|click>"}

func sample() submission {
	return submission{
		FormSlug: "contact", ID: "42",
		Fields: map[string]any{"name": "Ana", "email": "ana@example.com", "message": "Hi, can you call me"},
		Order:  []string{"name", "email", "message"},
	}
}

func withAdmin() cfg { return cfg{AdminURL: "https://example.com/admin/"} }

func sameJSON(t *testing.T, got []byte, want string) {
	t.Helper()
	var g, w any
	if err := json.Unmarshal(got, &g); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, got)
	}
	if err := json.Unmarshal([]byte(want), &w); err != nil {
		t.Fatalf("bad golden: %v", err)
	}
	if !reflect.DeepEqual(g, w) {
		t.Fatalf("got\n%s\nwant\n%s", got, want)
	}
}

func TestBuildViewLinkAndNoLink(t *testing.T) {
	v := buildView(sample(), withAdmin())
	if v.Link != "https://example.com/admin/settings/submissions/42" || v.Title != "New submission on contact" {
		t.Fatalf("view %+v", v)
	}
	if v := buildView(sample(), cfg{}); v.Link != "" || v.ID != "42" {
		t.Fatalf("no admin address: %+v", v)
	}
}

func TestBuildViewIncludeValuesOff(t *testing.T) {
	off := false
	v := buildView(sample(), cfg{IncludeValues: &off, AdminURL: "https://example.com/admin"})
	if len(v.Pairs) != 0 || v.Link == "" {
		t.Fatalf("view %+v", v)
	}
}

func TestBuildViewTruncates(t *testing.T) {
	s := sample()
	s.Fields["message"] = strings.Repeat("é", 50)
	v := buildView(s, cfg{MaxValueLength: 20})
	got := v.Pairs[2].Val
	if !strings.HasSuffix(got, "…") || utf8.RuneCountInString(got) != 21 {
		t.Fatalf("value %q", got)
	}
}

func TestBuildViewDefaultExclusion(t *testing.T) {
	s := submission{FormSlug: "f", ID: "1", Fields: map[string]any{
		"name": "a", "Password": "p", "api_token": "t", "card_number": "4", "ok": "y"}}
	keys := func(v view) string {
		var k []string
		for _, p := range v.Pairs {
			k = append(k, p.Key)
		}
		return strings.Join(k, ",")
	}
	if got := keys(buildView(s, cfg{})); got != "name,ok" {
		t.Fatalf("default: %s", got)
	}
	if got := keys(buildView(s, cfg{Fields: []string{"card_number", "name"}})); got != "card_number,name" {
		t.Fatalf("listed keys are kept: %s", got)
	}
}

func TestBuildViewNonStringValuesAndOrder(t *testing.T) {
	s := submission{FormSlug: "f", ID: "1", Order: []string{"b", "a"}, Fields: map[string]any{
		"a": true, "b": float64(1234567), "c": []any{"x", "y"}, "d": 1.5, "e": nil, "f": "  ", "g": "a\x00b\x07c\xff"}}
	v := buildView(s, cfg{})
	var got []string
	for _, p := range v.Pairs {
		got = append(got, p.Key+"="+p.Val)
	}
	want := "b=1234567,a=true,c=x, y,d=1.5,g=abc"
	if strings.Join(got, ",") != want {
		t.Fatalf("got %q want %q", strings.Join(got, ","), want)
	}
}

const slackGolden = `{"text":"New submission on contact (#42)","mrkdwn":false,"blocks":[
 {"type":"header","text":{"type":"plain_text","text":"New submission on contact","emoji":false}},
 {"type":"section","fields":[
  {"type":"plain_text","text":"name\nAna","emoji":false},
  {"type":"plain_text","text":"email\nana@example.com","emoji":false},
  {"type":"plain_text","text":"message\nHi, can you call me","emoji":false}]},
 {"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Open in admin"},"url":"https://example.com/admin/settings/submissions/42"}]}]}`

func TestSlackGolden(t *testing.T) {
	sameJSON(t, renderSlack(buildView(sample(), withAdmin())), slackGolden)
}

func TestSlackNoLinkShowsID(t *testing.T) {
	sameJSON(t, renderSlack(buildView(sample(), cfg{})), `{"text":"New submission on contact (#42)","mrkdwn":false,"blocks":[
 {"type":"header","text":{"type":"plain_text","text":"New submission on contact","emoji":false}},
 {"type":"section","fields":[
  {"type":"plain_text","text":"name\nAna","emoji":false},
  {"type":"plain_text","text":"email\nana@example.com","emoji":false},
  {"type":"plain_text","text":"message\nHi, can you call me","emoji":false}]},
 {"type":"section","text":{"type":"mrkdwn","text":"Submission #42"}}]}`)
}

func TestSlackValuesOff(t *testing.T) {
	off := false
	sameJSON(t, renderSlack(buildView(sample(), cfg{IncludeValues: &off, AdminURL: "https://example.com/admin"})), `{"text":"New submission on contact (#42)","mrkdwn":false,"blocks":[
 {"type":"header","text":{"type":"plain_text","text":"New submission on contact","emoji":false}},
 {"type":"actions","elements":[{"type":"button","text":{"type":"plain_text","text":"Open in admin"},"url":"https://example.com/admin/settings/submissions/42"}]}]}`)
}

func TestSlackSectionsHoldTenFields(t *testing.T) {
	f := map[string]any{}
	for _, k := range "abcdefghijkl" {
		f[string(k)] = "v"
	}
	var out struct {
		Blocks []struct {
			Type   string           `json:"type"`
			Fields []map[string]any `json:"fields"`
		} `json:"blocks"`
	}
	if err := json.Unmarshal(renderSlack(buildView(submission{FormSlug: "f", ID: "1", Fields: f}, cfg{})), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Blocks[1].Fields) != 10 || len(out.Blocks[2].Fields) != 2 {
		t.Fatalf("blocks %+v", out.Blocks)
	}
}

const teamsGolden = `{"type":"message","attachments":[{"contentType":"application/vnd.microsoft.card.adaptive","contentUrl":null,"content":{
 "$schema":"http://adaptivecards.io/schemas/adaptive-card.json","type":"AdaptiveCard","version":"1.4","msteams":{"width":"Full"},
 "body":[
  {"type":"TextBlock","text":"New submission on contact","weight":"Bolder","size":"Medium","wrap":true},
  {"type":"FactSet","facts":[{"title":"name","value":"Ana"},{"title":"email","value":"ana@example.com"},{"title":"message","value":"Hi, can you call me"}]}],
 "actions":[{"type":"Action.OpenUrl","title":"Open in admin","url":"https://example.com/admin/settings/submissions/42"}]}}]}`

func TestTeamsGolden(t *testing.T) {
	sameJSON(t, renderTeams(buildView(sample(), withAdmin())), teamsGolden)
}

func TestTeamsNoLinkShowsID(t *testing.T) {
	var out map[string]any
	b := renderTeams(buildView(sample(), cfg{}))
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(b), "Action.OpenUrl") || !strings.Contains(string(b), `Submission \\#42`) {
		t.Fatalf("body %s", b)
	}
}

func TestTeamsEscapesMarkdown(t *testing.T) {
	for _, in := range injections {
		s := sample()
		s.Fields["message"] = in
		var out struct {
			Attachments []struct {
				Content struct {
					Body []struct {
						Facts []struct{ Title, Value string } `json:"facts"`
					} `json:"body"`
				} `json:"content"`
			} `json:"attachments"`
		}
		if err := json.Unmarshal(renderTeams(buildView(s, cfg{})), &out); err != nil {
			t.Fatal(err)
		}
		val := out.Attachments[0].Content.Body[1].Facts[2].Value
		for i, r := range val {
			if (r == '[' || r == '(' || r == '<') && (i == 0 || val[i-1] != '\\') {
				t.Fatalf("%q renders %q: unescaped %q", in, val, r)
			}
		}
	}
}

func TestSlackFieldsAreLiteralText(t *testing.T) {
	for _, in := range append([]string{"*URGENT* :rotating_light: ~old~ https://evil.example/x"}, injections...) {
		s := sample()
		s.Fields["message"] = in
		var out struct {
			Blocks []struct {
				Fields []struct{ Type, Text string } `json:"fields"`
			} `json:"blocks"`
		}
		if err := json.Unmarshal(renderSlack(buildView(s, cfg{})), &out); err != nil {
			t.Fatal(err)
		}
		f := out.Blocks[1].Fields[2]
		if f.Type != "plain_text" || f.Text != "message\n"+in {
			t.Fatalf("%q renders %+v", in, f)
		}
	}
}

func TestBodyCapDropsTrailingPairs(t *testing.T) {
	f := map[string]any{}
	for i := 0; i < 50; i++ {
		f[string(rune('A'+i))] = strings.Repeat("x", 1900)
	}
	v := buildView(submission{FormSlug: "f", ID: "1", Fields: f}, cfg{MaxValueLength: 2000})
	for name, b := range map[string][]byte{"slack": renderSlack(v), "teams": renderTeams(v)} {
		if len(b) > maxBody || !json.Valid(b) {
			t.Fatalf("%s: %d bytes, valid %v", name, len(b), json.Valid(b))
		}
	}
}

func TestLinkNeedsAnHTTPSAdminAddress(t *testing.T) {
	for _, a := range []string{"javascript:alert(1)", "http://example.com/admin", "example.com/admin", "ftp://x"} {
		if v := buildView(sample(), cfg{AdminURL: a}); v.Link != "" {
			t.Errorf("admin address %q gave link %q", a, v.Link)
		}
	}
}
