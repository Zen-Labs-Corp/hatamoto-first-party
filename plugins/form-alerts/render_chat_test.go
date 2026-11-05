package main

import (
	"encoding/json"
	"regexp"
	"strings"
	"testing"
)

const gchatGolden = `{"text":"New submission on contact","cardsV2":[{"cardId":"submission-42","card":{
 "header":{"title":"New submission on contact"},
 "sections":[{"widgets":[
  {"decoratedText":{"topLabel":"name","text":"Ana","wrapText":true}},
  {"decoratedText":{"topLabel":"email","text":"ana@example.com","wrapText":true}},
  {"decoratedText":{"topLabel":"message","text":"Hi, can you call me","wrapText":true}},
  {"buttonList":{"buttons":[{"text":"Open in admin","onClick":{"openLink":{"url":"https://example.com/admin/settings/submissions/42"}}}]}}]}]}}]}`

func TestGChatGolden(t *testing.T) {
	sameJSON(t, renderGChat(buildView(sample(), withAdmin())), gchatGolden)
}

func TestGChatNoLinkShowsID(t *testing.T) {
	b := renderGChat(buildView(sample(), cfg{}))
	if strings.Contains(string(b), "openLink") || !strings.Contains(string(b), "Submission #42") {
		t.Fatalf("body %s", b)
	}
}

func TestGChatNewlinesBecomeBreaksAfterEscaping(t *testing.T) {
	s := sample()
	s.Fields["message"] = "a<b\nc"
	if !strings.Contains(string(renderGChat(buildView(s, cfg{}))), `a\u0026lt;b\u003cbr\u003ec`) {
		t.Fatalf("body %s", renderGChat(buildView(s, cfg{})))
	}
}

const telegramGolden = `{"chat_id":"-1001234","parse_mode":"HTML","link_preview_options":{"is_disabled":true},
"text":"<b>New submission on contact</b>\n<b>name:</b> <code>Ana</code>\n<b>email:</b> <code>ana@example.com</code>\n<b>message:</b> <code>Hi, can you call me</code>\n<a href=\"https://example.com/admin/settings/submissions/42\">Open in admin</a>"}`

func TestTelegramGolden(t *testing.T) {
	sameJSON(t, renderTelegram(buildView(sample(), withAdmin()), "-1001234"), telegramGolden)
}

func TestTelegramNoLinkShowsID(t *testing.T) {
	b := renderTelegram(buildView(sample(), cfg{}), "@mychannel")
	if strings.Contains(string(b), "<a ") || !strings.Contains(string(b), "Submission #42") || !strings.Contains(string(b), `"chat_id":"@mychannel"`) {
		t.Fatalf("body %s", b)
	}
	if strings.Contains(string(b), "token") {
		t.Fatalf("body mentions a token: %s", b)
	}
}

func TestTelegramCapsText(t *testing.T) {
	f := map[string]any{}
	for i := 0; i < 20; i++ {
		f[string(rune('A'+i))] = strings.Repeat("x", 500)
	}
	var out struct{ Text string }
	b := renderTelegram(buildView(submission{FormSlug: "f", ID: "1", Fields: f}, cfg{}), "1")
	if err := json.Unmarshal(b, &out); err != nil {
		t.Fatal(err)
	}
	if n := len([]rune(out.Text)); n > maxTelegramRunes || n < 1000 {
		t.Fatalf("text has %d characters", n)
	}
}

// renderers runs each renderer on a view and returns every text that carries
// submitted values, decoded.
func renderAll(v view) map[string][]byte {
	return map[string][]byte{
		"slack":    renderSlack(v),
		"teams":    renderTeams(v),
		"gchat":    renderGChat(v),
		"telegram": renderTelegram(v, "1"),
	}
}

var tagRe = regexp.MustCompile(`<[^>]*>`)

func TestInjectionsStayInertOnEveryPlatform(t *testing.T) {
	for _, in := range injections {
		s := sample()
		s.Fields["message"] = in
		s.Fields["name"] = in
		for name, b := range renderAll(buildView(s, withAdmin())) {
			if !json.Valid(b) {
				t.Fatalf("%s: invalid JSON for %q", name, in)
			}
			var doc any
			_ = json.Unmarshal(b, &doc)
			switch name {
			case "slack":
				// Field values are plain_text, which Slack does not parse;
				// TestSlackFieldsAreLiteralText covers them.
				for _, s := range leafStrings(slackWithoutFields(doc)) {
					if strings.ContainsAny(s, "<>") {
						t.Errorf("slack %q: raw angle bracket in %q", in, s)
					}
				}
			case "gchat":
				m := doc.(map[string]any)
				if m["text"] != "New submission on contact" {
					t.Errorf("gchat %q: text fallback %q", in, m["text"])
				}
				for _, s := range leafStrings(doc) {
					if tagRe.ReplaceAllString(strings.ReplaceAll(s, "<br>", ""), "") != strings.ReplaceAll(s, "<br>", "") {
						t.Errorf("gchat %q: markup in %q", in, s)
					}
				}
			case "telegram":
				text := doc.(map[string]any)["text"].(string)
				for _, tag := range tagRe.FindAllString(text, -1) {
					if !regexp.MustCompile(`^</?(b|code|a)>$|^<a href="[^"<>]*">$`).MatchString(tag) {
						t.Errorf("telegram %q: unexpected tag %q in %q", in, tag, text)
					}
				}
			}
		}
	}
}

// leafStrings returns every string value in a decoded document, except URLs
// that the plugin builds itself.
func leafStrings(v any) []string {
	var out []string
	switch x := v.(type) {
	case string:
		if !strings.HasPrefix(x, "http") {
			out = append(out, x)
		}
	case []any:
		for _, e := range x {
			out = append(out, leafStrings(e)...)
		}
	case map[string]any:
		for _, e := range x {
			out = append(out, leafStrings(e)...)
		}
	}
	return out
}

func TestSpecialCharactersKeepJSONValid(t *testing.T) {
	s := sample()
	s.Fields["message"] = "quote \" slash \\ line\nbreak 😀 tab\t end"
	for name, b := range renderAll(buildView(s, withAdmin())) {
		var doc any
		if err := json.Unmarshal(b, &doc); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
	}
}

// slackWithoutFields drops the plain_text fields from a Slack body.
func slackWithoutFields(doc any) any {
	m := doc.(map[string]any)
	var blocks []any
	for _, b := range m["blocks"].([]any) {
		if _, ok := b.(map[string]any)["fields"]; !ok {
			blocks = append(blocks, b)
		}
	}
	return map[string]any{"text": m["text"], "blocks": blocks}
}
