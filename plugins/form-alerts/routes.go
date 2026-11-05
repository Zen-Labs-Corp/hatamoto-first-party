package main

import (
	"encoding/json"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// testBudget is how long Send test message keeps starting requests; the
// route's own limit is 10 s.
const testBudget = 8 * time.Second

// answerMax caps the platform answer shown on the admin page.
const answerMax = 200

func init() { guest.OnHTTP(serve) }

// serve answers the owner's admin page. A problem is a JSON answer, never a
// Go error (the host would turn that into a bare 502).
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /log":
		return logRoute()
	case "POST /test":
		return testRoute(r)
	}
	return reply(http.StatusNotFound, map[string]any{"error": "not found"}), nil
}

func reply(code int, v any) sdk.HTTPResponse {
	b, _ := json.Marshal(v)
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "application/json"},
		Body:    string(b),
	}
}

// formInfo is one form as the admin page shows it.
type formInfo struct {
	Slug   string   `json:"slug"`
	Name   string   `json:"name"`
	Fields []string `json:"fields"`
}

// listFormInfo returns the forms core knows, in core's order.
func listFormInfo() []formInfo {
	var res struct {
		Items []struct {
			Slug   string `json:"slug"`
			Name   string `json:"name"`
			Fields []struct {
				Name string `json:"name"`
			} `json:"fields"`
		} `json:"items"`
	}
	out := []formInfo{}
	if callHost("forms.list", struct{}{}, &res) != nil {
		return out
	}
	for _, f := range res.Items {
		fi := formInfo{Slug: f.Slug, Name: f.Name, Fields: []string{}}
		for _, fd := range f.Fields {
			fi.Fields = append(fi.Fields, fd.Name)
		}
		out = append(out, fi)
	}
	return out
}

// logRoute returns channel status (never a secret), the settings summary, a
// preview of the latest submission and the last 100 log rows.
func logRoute() (sdk.HTTPResponse, error) {
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "settings unreadable"}), nil
	}
	var rows json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb)
FROM (SELECT created_at, form_slug, channel, status, code FROM log ORDER BY id DESC LIMIT 100) t`, &rows)
	if err != nil {
		return reply(http.StatusServiceUnavailable, map[string]any{"error": "delivery log unreadable"}), nil
	}
	forms := listFormInfo()
	return reply(http.StatusOK, map[string]any{
		"channels": map[string]any{
			chanSlack:    map[string]any{"enabled": c.SlackEnabled, "set": c.isSet(chanSlack)},
			chanTeams:    map[string]any{"enabled": c.TeamsEnabled, "set": c.isSet(chanTeams)},
			chanGChat:    map[string]any{"enabled": c.GChatEnabled, "set": c.isSet(chanGChat)},
			chanTelegram: map[string]any{"enabled": c.TelegramEnabled, "set": c.isSet(chanTelegram), "chat_id": strings.TrimSpace(c.TelegramChatID)},
		},
		"forms": forms,
		"settings": map[string]any{
			"forms":            nonNil(c.Forms),
			"fields":           nonNil(c.Fields),
			"include_values":   c.includeValues(),
			"max_value_length": c.maxLen(),
			"admin_url":        c.AdminURL,
		},
		"preview": preview(c, forms),
		"rows":    rows,
	}), nil
}

func nonNil(s []string) []string {
	if s == nil {
		return []string{}
	}
	return s
}

// preview shows the latest non-spam submission of the first watched form,
// built the same way as a real message. It is nil when there is none.
func preview(c cfg, forms []formInfo) any {
	var form *formInfo
	for i := range forms {
		if c.watched(forms[i].Slug) {
			form = &forms[i]
			break
		}
	}
	if form == nil {
		return nil
	}
	var res struct {
		Items []struct {
			ID     json.RawMessage `json:"id"`
			Status string          `json:"status"`
			Data   map[string]any  `json:"data"`
		} `json:"items"`
	}
	if callHost("forms.submissions", map[string]any{"form_slug": form.Slug, "limit": 1}, &res) != nil {
		return nil
	}
	for _, it := range res.Items {
		if it.Status == "spam" {
			continue
		}
		id := strings.Trim(string(it.ID), `"`)
		v := buildView(submission{FormSlug: form.Slug, ID: id, Fields: it.Data, Order: form.Fields}, c)
		pairs := make([]map[string]string, 0, len(v.Pairs))
		for _, p := range v.Pairs {
			pairs = append(pairs, map[string]string{"key": p.Key, "value": p.Val})
		}
		return map[string]any{"form": form.Slug, "title": v.Title, "pairs": pairs, "link": v.Link}
	}
	return nil
}

// testRoute posts a sample message to one channel and shows the platform's
// answer with every secret removed.
func testRoute(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var in struct {
		Channel string `json:"channel"`
	}
	_ = json.Unmarshal([]byte(r.Body), &in)
	switch in.Channel {
	case chanSlack, chanTeams, chanGChat, chanTelegram:
	default:
		return reply(http.StatusBadRequest, map[string]any{"error": "unknown channel"}), nil
	}
	c, err := loadCfg()
	if err != nil {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "settings unreadable"}), nil
	}
	if !c.isSet(in.Channel) {
		return reply(http.StatusOK, map[string]any{"ok": false, "error": "not set up"}), nil
	}
	v := view{
		Title: "Test message from Form alerts",
		Pairs: []pair{{Key: "name", Val: "Sample sender"}, {Key: "message", Val: "This is a test."}},
		Link:  adminBase(c),
	}
	budgetEnd = now().Add(testBudget)
	defer func() { budgetEnd = time.Time{} }()
	res := post(c, in.Channel, string(renderFor(in.Channel, v, c)))
	if res.Code != "" {
		return reply(http.StatusOK, map[string]any{"ok": false, "status": 0, "answer": hostWords(res.Code)}), nil
	}
	ans := sanitize(res.Body, c.SlackWebhookURL, c.TeamsWorkflowURL, c.GChatWebhookURL, c.TelegramBotToken, c.TelegramChatID)
	return reply(http.StatusOK, map[string]any{
		"ok":     res.Status >= 200 && res.Status <= 299,
		"status": res.Status,
		"answer": ans,
	}), nil
}

// hostWords is a fixed phrase for a failed call, never the host's text.
func hostWords(code string) string {
	switch code {
	case budgetCode, sdk.ErrTimeout:
		return "timed out"
	case sdk.ErrBlocked, sdk.ErrCapabilityDenied:
		return "blocked"
	}
	return "network error"
}

var secretQuery = regexp.MustCompile(`(?i)\b(key|token|sig)=[^&\s"'<>]*`)

// sanitize removes every secret from a platform answer and caps its length.
// Secrets are the webhook URLs, the bot token and the chat id, plus the value
// of any key=, token= or sig= query parameter, including URL-decoded forms.
func sanitize(answer string, secrets ...string) string {
	for _, s := range secrets {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		answer = strings.ReplaceAll(answer, s, "")
		if u, err := url.Parse(s); err == nil && u.Host != "" {
			answer = strings.ReplaceAll(answer, u.Host+u.EscapedPath(), "")
		}
	}
	answer = secretQuery.ReplaceAllString(answer, "$1=")
	answer = strings.TrimSpace(answer)
	if rs := []rune(answer); len(rs) > answerMax {
		answer = string(rs[:answerMax])
	}
	return answer
}
