package main

import (
	"encoding/json"
	"regexp"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// actionApprove is the action of edit_dates that stages one submitted event.
// The run gets the public fields only; it cannot read the database, so the
// submitter's email never reaches the changeset.
const actionApprove = "approve"

var submissionIDRe = regexp.MustCompile(`^[0-9]{1,18}$`)

// approveSubmission stages the submission as a new event. The owner reviews
// it in Changes and publishes it there; the entity hook then marks the
// submission published. A submission staged twice shares its source key, so
// the second run finds the key taken and says so.
func approveSubmission(p map[string]any) (sdk.RunStepResult, error) {
	id := paramString(p, "submission")
	if !submissionIDRe.MatchString(id) {
		return failRun("The submission input must be the number of a submission.")
	}
	title := capRunes(paramString(p, "title"), maxSubmitTitle)
	if title == "" {
		return failRun("The title is missing.")
	}
	s := loadSettings()
	start, ok := parseTime(paramString(p, "start_at"), s.location())
	if !ok {
		return failRun("The start must look like 2026-03-03T18:30.")
	}
	attrs := map[string]json.RawMessage{}
	set := func(k, v string) {
		if v != "" {
			attrs[k], _ = json.Marshal(v)
		}
	}
	set("start_at", start.Format(time.RFC3339))
	if e := paramString(p, "end_at"); e != "" {
		end, ok := parseTime(e, s.location())
		if !ok || end.Before(start) {
			return failRun("The end must look like 2026-03-03T20:00 and not be before the start.")
		}
		set("end_at", end.Format(time.RFC3339))
	}
	set("venue_name", capBytes(paramString(p, "venue_name"), maxSubmitVenue))
	set("description", capBytes(paramString(p, "description"), maxSubmitDesc))
	key := "sub-" + id
	set("source_kind", "submission")
	set("source_uid", key)

	raw, _ := json.Marshal(attrs)
	res, err := guest.EntityUpsert(callHost, sdk.EntityUpsert{SourceKey: key, Schema: s.slugs()[0], Name: title, Attributes: raw})
	if err != nil {
		return failRun("The event could not be staged. Try again.")
	}
	if !res.Created {
		return failRun("This submission was already staged.")
	}
	return sdk.RunStepResult{}, guest.RunFinish(callHost, map[string]any{"staged": 1})
}
