package main

import (
	"encoding/json"
	"strings"
)

// gchatEsc turns submitted text into inert HTML for card widgets: no tags, no
// links, no <users/all>.
var gchatEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// gchatText escapes, then turns newlines into <br>.
func gchatText(s string) string {
	return strings.ReplaceAll(gchatEsc.Replace(s), "\n", "<br>")
}

// gchatPlain removes the characters that Chat reads as markup from text that
// is shown outside a card.
func gchatPlain(s string) string {
	return strings.NewReplacer("<", "", ">", "", "&", "").Replace(s)
}

// renderGChat builds an incoming-webhook body: a card with the fields and a
// static text fallback. The fallback never carries submitted values, because
// Chat applies mentions and links to plain text.
func renderGChat(v view) []byte { return fitBody(v, gchatBody) }

func gchatBody(v view) []byte {
	var widgets []any
	for _, p := range v.Pairs {
		widgets = append(widgets, map[string]any{"decoratedText": map[string]any{
			"topLabel": gchatText(p.Key), "text": gchatText(p.Val), "wrapText": true,
		}})
	}
	if v.Link != "" {
		widgets = append(widgets, map[string]any{"buttonList": map[string]any{"buttons": []any{map[string]any{
			"text":    "Open in admin",
			"onClick": map[string]any{"openLink": map[string]any{"url": v.Link}},
		}}}})
	} else if v.ID != "" {
		widgets = append(widgets, map[string]any{"textParagraph": map[string]any{"text": gchatText(idLine(v))}})
	}
	card := map[string]any{"header": map[string]any{"title": v.Title}}
	if len(widgets) > 0 {
		card["sections"] = []any{map[string]any{"widgets": widgets}}
	}
	b, _ := json.Marshal(map[string]any{
		"text":    gchatPlain(v.Title),
		"cardsV2": []any{map[string]any{"cardId": cardID(v.ID), "card": card}},
	})
	return b
}

// cardID is "submission-" plus the digits of the id.
func cardID(id string) string {
	d := strings.Map(func(r rune) rune {
		if r >= '0' && r <= '9' {
			return r
		}
		return -1
	}, id)
	if d == "" {
		return "submission"
	}
	return "submission-" + d
}
