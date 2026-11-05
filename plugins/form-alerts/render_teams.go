package main

import (
	"encoding/json"
	"strings"
)

// teamsEsc backslash-escapes the markdown Teams applies to card text, so a
// submitted [label](url) or *bold* shows as typed.
var teamsEsc = strings.NewReplacer(`\`, `\\`, "`", "\\`", "*", `\*`, "_", `\_`, "[", `\[`, "]", `\]`, "(", `\(`, ")", `\)`, "#", `\#`, "<", `\<`, ">", `\>`, "~", `\~`, "|", `\|`)

// teamsText escapes and turns newlines into paragraph breaks.
func teamsText(s string) string {
	return strings.ReplaceAll(teamsEsc.Replace(s), "\n", "\n\n")
}

// renderTeams builds the body of a Workflows "when a webhook request is
// received" trigger: a message holding one Adaptive Card.
func renderTeams(v view) []byte { return fitBody(v, teamsBody) }

func teamsBody(v view) []byte {
	body := []any{map[string]any{
		"type": "TextBlock", "text": teamsText(v.Title), "weight": "Bolder", "size": "Medium", "wrap": true,
	}}
	if len(v.Pairs) > 0 {
		facts := make([]any, 0, len(v.Pairs))
		for _, p := range v.Pairs {
			facts = append(facts, map[string]any{"title": teamsText(p.Key), "value": teamsText(p.Val)})
		}
		body = append(body, map[string]any{"type": "FactSet", "facts": facts})
	}
	card := map[string]any{
		"$schema": "http://adaptivecards.io/schemas/adaptive-card.json",
		"type":    "AdaptiveCard",
		"version": "1.4",
		"msteams": map[string]any{"width": "Full"},
	}
	if v.Link != "" {
		card["actions"] = []any{map[string]any{"type": "Action.OpenUrl", "title": "Open in admin", "url": v.Link}}
	} else if v.ID != "" {
		body = append(body, map[string]any{"type": "TextBlock", "text": teamsText(idLine(v)), "wrap": true})
	}
	card["body"] = body
	b, _ := json.Marshal(map[string]any{
		"type": "message",
		"attachments": []any{map[string]any{
			"contentType": "application/vnd.microsoft.card.adaptive",
			"contentUrl":  nil,
			"content":     card,
		}},
	})
	return b
}
