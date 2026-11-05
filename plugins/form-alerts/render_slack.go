package main

import (
	"encoding/json"
	"strings"
)

// slackEsc neutralises Slack's control characters: it defuses <!channel>,
// <!here>, <@user> and <url|label> in submitted text.
var slackEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// raw leaves text as it is, for plain_text fields: Slack reads no markup or
// entities there, so submitted bold, emoji and mentions stay literal.
func raw(s string) string { return s }

func slackText(s string) string { return slackEsc.Replace(s) }

// renderSlack builds an incoming-webhook body: Block Kit plus a plain text
// fallback for notifications.
func renderSlack(v view) []byte { return fitBody(v, slackBody) }

func slackBody(v view) []byte {
	title := slackText(v.Title)
	text := title
	if v.ID != "" {
		text += " (#" + slackText(v.ID) + ")"
	}
	blocks := []any{map[string]any{
		"type": "header",
		"text": map[string]any{"type": "plain_text", "text": fit(v.Title, raw, 150), "emoji": false},
	}}
	for i := 0; i < len(v.Pairs); i += 10 {
		var fields []any
		for _, p := range v.Pairs[i:min(i+10, len(v.Pairs))] {
			fields = append(fields, map[string]any{
				"type":  "plain_text",
				"text":  fit(p.Key, raw, 200) + "\n" + fit(p.Val, raw, 1700),
				"emoji": false,
			})
		}
		blocks = append(blocks, map[string]any{"type": "section", "fields": fields})
	}
	if v.Link != "" {
		blocks = append(blocks, map[string]any{"type": "actions", "elements": []any{map[string]any{
			"type": "button",
			"text": map[string]any{"type": "plain_text", "text": "Open in admin"},
			"url":  v.Link,
		}}})
	} else if v.ID != "" {
		blocks = append(blocks, map[string]any{"type": "section", "text": map[string]any{"type": "mrkdwn", "text": slackText(idLine(v))}})
	}
	b, _ := json.Marshal(map[string]any{"text": text, "mrkdwn": false, "blocks": blocks})
	return b
}
