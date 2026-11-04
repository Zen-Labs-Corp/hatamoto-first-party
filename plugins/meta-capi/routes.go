package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnHTTP(serve)
}

// serve answers the two admin routes. Neither is public.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	switch r.Method + " " + r.Pattern {
	case "GET /log":
		return getLog()
	case "POST /test":
		return postTest()
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

func invalid(msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusBadRequest, map[string]string{"error": msg})
}

// fail logs the host error text and answers 500 without details.
func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "meta-capi: request failed: %s", cut(err.Error(), maxHostErr))
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

// getLog returns the configuration summary and the newest 200 deliveries. The
// token is never part of it, only the names of missing settings.
func getLog() (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	var rows json.RawMessage
	err = dbJSON(`SELECT coalesce(jsonb_agg(to_jsonb(t)), '[]'::jsonb) FROM (
SELECT submission_id, form_slug, status, reason, meta_code, meta_message, test, created_at
FROM deliveries ORDER BY created_at DESC LIMIT 200) t`, &rows)
	if err != nil {
		return fail(err)
	}
	missing := s.missing()
	if missing == nil {
		missing = []string{}
	}
	forms := s.Forms
	if forms == nil {
		forms = []string{}
	}
	var perms struct {
		Granted []string `json:"granted"`
	}
	formsRead := callHost("permissions", struct{}{}, &perms) == nil && slices.Contains(perms.Granted, "forms.read")
	return reply(http.StatusOK, map[string]any{
		"configured":         len(missing) == 0,
		"missing":            missing,
		"pixel_id":           s.PixelID,
		"graph_version":      s.GraphVersion,
		"event_name":         s.EventName,
		"forms":              forms,
		"test_event_code":    s.TestEventCode,
		"forms_read_granted": formsRead,
		"rows":               rows,
	})
}

// settingLabels names missing settings the way the Settings page does.
func settingLabels(missing []string) string {
	names := map[string]string{"pixel_id": "Pixel ID", "access_token": "Access token", "graph_version": "a valid Graph API version"}
	out := make([]string, len(missing))
	for i, m := range missing {
		out[i] = names[m]
	}
	return strings.Join(out, ", ")
}

// postTest sends one test event (hash of a fixed example address, no visitor
// data) to the test event code from the settings and logs it as a test row.
func postTest() (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	if s.TestEventCode == "" {
		return invalid("Set a Test event code in Settings first (Events Manager > Test events).")
	}
	if m := s.missing(); len(m) > 0 {
		return invalid("Fill in " + settingLabels(m) + " in Settings first.")
	}
	now := time.Now()
	ev := sdk.FormSubmittedEvent{
		FormSlug:     "test",
		SubmissionID: fmt.Sprintf("test-%d", now.UnixMilli()),
		SubmittedAt:  now,
	}
	ud := userData{Em: []string{sha("test@example.com")}}
	inserted, err := insertDelivery(ev.SubmissionID, ev.FormSlug, "failed", "interrupted", true)
	if err != nil {
		return fail(err)
	}
	if !inserted {
		return reply(http.StatusConflict, map[string]string{"error": "a test event was just sent; try again in a moment"})
	}
	o := sendEvent(s, ev, ud, s.TestEventCode)
	logFailure(o)
	if err := finishDelivery(ev.SubmissionID, o); err != nil {
		logf("error", "meta-capi: delivery log update failed: %s", cut(err.Error(), maxHostErr))
	}
	if o.Status != "sent" {
		msg := o.Message
		if msg == "" {
			msg = "Meta could not be reached."
		}
		return reply(http.StatusBadGateway, map[string]any{"error": msg, "code": o.Code})
	}
	return reply(http.StatusOK, map[string]any{"ok": true, "code": o.Code})
}
