package main

import (
	"encoding/json"
	"strconv"
)

const tiktokURL = "https://business-api.tiktok.com/open_api/v1.3/event/track/"

// buildTikTok is the Events API request for one conversion. event_id is the
// submission id, so the browser pixel's event for the same submission is
// deduplicated. The access token travels only in the header.
func buildTikTok(s settings, j job, test bool) request {
	t := j.SubmittedAt
	if t.IsZero() {
		t = now()
	}
	user := map[string]any{}
	if j.EmHash != "" {
		user["email"] = j.EmHash
	}
	if j.PhHash != "" {
		user["phone"] = j.PhHash
	}
	if v := j.IDs["ttclid"]; v != "" {
		user["ttclid"] = v
	}
	ev := map[string]any{
		"event":      s.TikTok.Event,
		"event_time": t.Unix(),
		"event_id":   j.SubmissionID,
		"user":       user,
	}
	if s.PublicURL != "" {
		ev["page"] = map[string]any{"url": s.PublicURL + j.PagePath}
	}
	props := map[string]any{}
	if s.Value != nil {
		props["value"] = *s.Value
	}
	if s.Currency != "" {
		props["currency"] = s.Currency
	}
	if len(props) > 0 {
		ev["properties"] = props
	}
	body := map[string]any{
		"event_source":    "web",
		"event_source_id": s.TikTok.PixelCode,
		"data":            []any{ev},
	}
	// The code marks events as test events in Events Manager, so it goes only
	// with the Test button, never with a live conversion.
	if test && s.TikTok.TestEventCode != "" {
		body["test_event_code"] = s.TikTok.TestEventCode
	}
	b, _ := json.Marshal(body)
	return request{
		Method:  "POST",
		URL:     tiktokURL,
		Headers: map[string]string{"Content-Type": "application/json", "Access-Token": s.TikTok.AccessToken},
		Body:    b,
	}
}

// hasIdentifierTikTok reports whether TikTok has something to match on.
func hasIdentifierTikTok(j job) bool {
	return j.EmHash != "" || j.PhHash != "" || j.IDs["ttclid"] != ""
}

// classifyTikTok reads TikTok's answer: HTTP 200 carries a code in the body, 0
// meaning accepted. Any other HTTP status is classified as plain HTTP.
func classifyTikTok(status int, body []byte) outcome {
	if status != 200 {
		o := classifyHTTP(status, false)
		if o.Kind == kindSent {
			o.Kind, o.Reason = kindFailed, "http"
		}
		return o
	}
	var r struct {
		Code    *int   `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(body, &r) != nil || r.Code == nil {
		return outcome{Kind: kindFailed, Reason: "platform", Code: "200", Message: "unreadable answer"}
	}
	code := *r.Code
	o := outcome{Code: strconv.Itoa(code), Message: cut(r.Message, maxMessage)}
	switch {
	case code == 0:
		o.Kind = kindSent
		o.Message = ""
	case code == 40001 || (code >= 40101 && code <= 40105):
		o.Kind, o.Reason = kindAuth, "platform"
	// 40100 is "requests made too frequently", a rate limit.
	case code == 40016 || code == 40100 || code == 50000 || (code >= 51000 && code < 52000):
		o.Kind, o.Reason = kindRetry, "platform"
	default:
		o.Kind, o.Reason = kindFailed, "platform"
	}
	return o
}
