// Command ads-conversions is a Hatamoto plugin. Sends a server-side conversion to Google Ads, TikTok and LinkedIn for each form submission, with hashed contact details and the ad click ID, to back up the browser tags.
//
// The form hook only queues what it finds (hashes and click IDs, never a raw
// value); cron jobs send the conversions, so a slow platform never holds up a
// form. The logic is plain Go in these files; the //go:wasmexport lines live
// in exports_wasip1.go, so everything but the exports builds and tests on the
// host.
package main

import (
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.FormSubmitted, onFormSubmitted)
}

// truthy reads a consent value: true, a non-zero number, or one of 1, true,
// yes, on, y, checked in any case. A non-empty list (checkbox options store
// the chosen values) counts as consent.
func truthy(v any) bool {
	switch x := v.(type) {
	case bool:
		return x
	case float64:
		return x != 0
	case []any:
		return len(x) > 0
	case string:
		switch strings.ToLower(strings.TrimSpace(x)) {
		case "1", "true", "yes", "on", "y", "checked":
			return true
		}
	}
	return false
}

// isBroken reports whether the platform's connection needs reconnecting. A
// host error counts as not broken, so the hook fails open.
func isBroken(s settings, platform string) bool {
	var v string
	found, err := kvGet(statusKeyPrefix+platform, &v)
	return err == nil && found && v == statusValue(s, platform)
}

// onFormSubmitted queues one conversion per active platform. It returns nil on
// every path so a problem here never trips the host's circuit breaker.
func onFormSubmitted(ev sdk.FormSubmittedEvent) error {
	defer func() {
		if recover() != nil {
			logf("error", "ads-conversions: queueing stopped by an internal error")
		}
	}()
	queueSubmission(ev)
	return nil
}

func queueSubmission(ev sdk.FormSubmittedEvent) {
	s, err := loadSettings()
	if err != nil {
		logf("warn", "ads-conversions: settings unreadable")
		return
	}
	if !s.allowsForm(ev.FormSlug) {
		return
	}
	var active []string
	for _, p := range platforms {
		if s.enabled(p) && s.configured(p) && !isBroken(s, p) {
			active = append(active, p)
		}
	}
	if len(active) == 0 {
		return
	}
	if s.ConsentField != "" && !truthy(ev.Fields[s.ConsentField]) {
		recordOnce(ev.SubmissionID, ev.FormSlug, "-", "skipped", "consent")
		return
	}
	id := buildIdentity(ev.Fields, s.EmailField, s.PhoneField, s.DefaultCountryCode)
	// Without a consent field there is no consent signal, so the page address
	// is not read: only the click IDs the page script posts after consent
	// (joined at send time) are used.
	var ids clickIDs
	if s.ConsentField != "" {
		ids = idsFromSourcePath(ev.SourcePath)
	}
	// A submission with nothing to match on is still queued: the page script
	// may post a click ID within a minute, and the send step decides.
	for _, p := range active {
		_, err := enqueue(job{
			SubmissionID: ev.SubmissionID, Form: ev.FormSlug, Platform: p,
			EmHash: id.Em, EmGoogleHash: id.EmGoogle, PhHash: id.Ph, IDs: ids,
			PagePath: cleanPath(ev.SourcePath), Consent: s.ConsentField != "", SubmittedAt: ev.SubmittedAt,
		})
		if err != nil {
			logf("warn", "ads-conversions: could not queue a conversion")
		}
	}
}
