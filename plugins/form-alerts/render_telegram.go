package main

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

// tgEsc escapes the three characters Telegram's HTML mode reads.
var tgEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;")

// tgAttr also escapes the quote, for an attribute value.
func tgAttr(s string) string { return strings.ReplaceAll(tgEsc.Replace(s), `"`, "&quot;") }

// maxTelegramRunes keeps well under the 4096 character limit of a message.
const maxTelegramRunes = 3500

// renderTelegram builds a sendMessage body. Values sit in <code> so that an
// @name or a bare link in them is neither a mention nor an autolink. The bot
// token is not part of the body: it goes into the request URL at send time.
func renderTelegram(v view, chatID string) []byte {
	for {
		text := telegramText(v)
		if utf8.RuneCountInString(text) <= maxTelegramRunes || len(v.Pairs) == 0 {
			b, _ := json.Marshal(map[string]any{
				"chat_id":              chatID,
				"text":                 text,
				"parse_mode":           "HTML",
				"link_preview_options": map[string]any{"is_disabled": true},
			})
			return b
		}
		v.Pairs = v.Pairs[:len(v.Pairs)-1]
	}
}

func telegramText(v view) string {
	var b strings.Builder
	b.WriteString("<b>" + tgEsc.Replace(v.Title) + "</b>")
	for _, p := range v.Pairs {
		b.WriteString("\n<b>" + tgEsc.Replace(p.Key) + ":</b> <code>" + tgEsc.Replace(p.Val) + "</code>")
	}
	if v.Link != "" {
		b.WriteString("\n<a href=\"" + tgAttr(v.Link) + "\">Open in admin</a>")
	} else if v.ID != "" {
		b.WriteString("\n" + tgEsc.Replace(idLine(v)))
	}
	return b.String()
}
