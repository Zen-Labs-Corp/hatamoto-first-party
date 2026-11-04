package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// outcome is the result of one delivery as stored in the log. It never holds
// a field value, a hash or the token.
type outcome struct {
	Status  string // sent, failed or skipped
	Reason  string // short code, empty when sent
	Code    *int   // HTTP status or Meta error code
	Message string // Meta's error.message, cut to maxMessage runes
	Err     string // host error text for the log line only, not stored
}

const (
	maxMessage = 300
	maxHostErr = 200
)

// cut shortens s to at most n runes.
func cut(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n])
}

// maxPath caps the path part of event_source_url.
const maxPath = 512

// cleanPath reduces the client's source path to a safe path: an absolute
// http(s) URL loses its scheme, host, query and fragment, a plain path loses
// its query and fragment. Anything that is not a clean path with a leading
// "/" gives "", so event_source_url falls back to the site URL alone.
func cleanPath(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" || len(raw) > 2048 {
		return ""
	}
	for _, r := range raw {
		if r <= ' ' || r == 0x7f || r == '\\' {
			return ""
		}
	}
	if i := strings.IndexAny(raw, "?#"); i >= 0 {
		raw = raw[:i]
	}
	if strings.HasPrefix(raw, "http://") || strings.HasPrefix(raw, "https://") {
		u, err := url.Parse(raw)
		if err != nil || u.Host == "" {
			return ""
		}
		raw = u.EscapedPath()
	}
	if !strings.HasPrefix(raw, "/") || strings.HasPrefix(raw, "//") || len(raw) > maxPath {
		return ""
	}
	return raw
}

// buildEvent is the Conversions API request body for one submission. The token
// goes in the body, not the URL.
func buildEvent(s settings, ev sdk.FormSubmittedEvent, ud userData, testCode string) []byte {
	t := ev.SubmittedAt
	if t.IsZero() {
		t = time.Now()
	}
	data := map[string]any{
		"event_name":    s.EventName,
		"event_time":    t.Unix(),
		"event_id":      ev.SubmissionID,
		"action_source": "website",
		"user_data":     ud,
		"custom_data":   map[string]any{"form": ev.FormSlug},
	}
	if s.SiteURL != "" {
		data["event_source_url"] = s.SiteURL + cleanPath(ev.SourcePath)
	}
	body := map[string]any{
		"data":         []any{data},
		"access_token": s.AccessToken,
	}
	if testCode != "" {
		body["test_event_code"] = testCode
	}
	b, _ := json.Marshal(body)
	return b
}

// parseMeta turns Meta's answer into an outcome: any 2xx is sent.
func parseMeta(f fetched) outcome {
	code := f.Status
	if f.Status >= 200 && f.Status < 300 {
		return outcome{Status: "sent", Code: &code}
	}
	o := outcome{Status: "failed", Reason: "http_error", Code: &code}
	var e struct {
		Error struct {
			Code    *int   `json:"code"`
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal([]byte(f.Body), &e) == nil {
		if e.Error.Code != nil {
			o.Code = e.Error.Code
		}
		o.Message = cut(e.Error.Message, maxMessage)
	}
	return o
}

// insertDelivery writes the log row. inserted is false when the submission
// already has a row, which means it was handled before. Rows older than 30
// days are removed after every new row; a failed prune is only logged.
func insertDelivery(submissionID, form, status, reason string, test bool) (inserted bool, err error) {
	n, err := dbExecRows(`INSERT INTO deliveries (submission_id, form_slug, status, reason, test)
VALUES ($1, $2, $3, $4, $5) ON CONFLICT (submission_id) DO NOTHING`, submissionID, form, status, reason, test)
	if err != nil || n == 0 {
		return false, err
	}
	if err := dbExec(`DELETE FROM deliveries WHERE created_at < now() - interval '30 days'`); err != nil {
		logf("error", "meta-capi: pruning the delivery log failed: %s", cut(err.Error(), maxHostErr))
	}
	return true, nil
}

// finishDelivery stores the final result on the row insertDelivery wrote.
func finishDelivery(submissionID string, o outcome) error {
	var code, msg any
	if o.Code != nil {
		code = *o.Code
	}
	if o.Message != "" {
		msg = o.Message
	}
	return dbExec(`UPDATE deliveries SET status = $2, reason = $3, meta_code = $4, meta_message = $5
WHERE submission_id = $1`, submissionID, o.Status, o.Reason, code, msg)
}

// hostReason names why the host refused http.fetch by its stable error code
// (and the capability it names), never by its free text.
func hostReason(err error) string {
	var he *sdk.HostError
	if !errors.As(err, &he) || he.Code == "" {
		return ""
	}
	m := "host: " + he.Code
	if he.Capability != "" {
		m += " " + he.Capability
	}
	return cut(m, maxMessage)
}

// sendEvent posts the event once, without retries.
func sendEvent(s settings, ev sdk.FormSubmittedEvent, ud userData, testCode string) outcome {
	res, err := postJSON(s.endpoint(), buildEvent(s, ev, ud, testCode))
	if err != nil {
		return outcome{Status: "failed", Reason: "http_error", Message: hostReason(err), Err: cut(err.Error(), maxHostErr)}
	}
	return parseMeta(res)
}

// logFailure writes the one log line of a failed delivery: codes only, no
// values and no Meta message.
func logFailure(o outcome) {
	if o.Status != "failed" {
		return
	}
	switch {
	case o.Err != "":
		logf("error", "meta-capi: delivery failed: %s", o.Err)
	case o.Code != nil:
		logf("error", "meta-capi: delivery failed: Meta answered with code %d", *o.Code)
	default:
		logf("error", "meta-capi: delivery failed")
	}
}

// truthy reads a consent value: true, a non-zero number, or one of 1, true,
// yes, on, y, checked in any case.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "on", "y", "checked":
			return true
		}
	}
	return false
}

// onFormSubmitted is the form.submitted action. It returns nil on every path
// so a problem here never trips the host's circuit breaker.
func onFormSubmitted(ev sdk.FormSubmittedEvent) error {
	defer func() {
		if recover() != nil {
			logf("error", "meta-capi: delivery stopped by an internal error")
		}
	}()
	deliver(ev)
	return nil
}

// deliver handles one submission: skip it, or record it and send it once.
func deliver(ev sdk.FormSubmittedEvent) {
	s, err := loadSettings()
	if err != nil {
		logf("error", "meta-capi: settings unreadable: %s", cut(err.Error(), maxHostErr))
		return
	}
	skip := func(reason string) {
		if _, err := insertDelivery(ev.SubmissionID, ev.FormSlug, "skipped", reason, false); err != nil {
			logf("error", "meta-capi: delivery log unavailable: %s", cut(err.Error(), maxHostErr))
		}
	}
	if len(s.missing()) > 0 {
		skip("not_configured")
		return
	}
	if !s.allowsForm(ev.FormSlug) {
		skip("form_filtered")
		return
	}
	if s.ConsentField != "" && !truthy(ev.Fields[s.ConsentField]) {
		skip("consent")
		return
	}
	ud := buildUserData(ev.Fields, s.EmailField, s.PhoneField, s.NameField)
	if !ud.hasContact() {
		skip("no_contact")
		return
	}
	// The row goes in first: it is the dedupe key, and if the module stops
	// before the update the row honestly says the send state is unknown.
	inserted, err := insertDelivery(ev.SubmissionID, ev.FormSlug, "failed", "interrupted", false)
	if err != nil {
		logf("error", "meta-capi: delivery log unavailable: %s", cut(err.Error(), maxHostErr))
		return
	}
	if !inserted {
		return
	}
	o := sendEvent(s, ev, ud, s.TestEventCode)
	logFailure(o)
	if err := finishDelivery(ev.SubmissionID, o); err != nil {
		logf("error", "meta-capi: delivery log update failed: %s", cut(err.Error(), maxHostErr))
	}
}
