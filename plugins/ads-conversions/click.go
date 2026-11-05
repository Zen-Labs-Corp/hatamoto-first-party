package main

import (
	"encoding/json"
	"net/http"
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
)

const maxClickBody = 4096

var (
	uuidRe     = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	formSlugRe = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
)

func noContent() (sdk.HTTPResponse, error) {
	return sdk.HTTPResponse{Status: http.StatusNoContent}, nil
}

// postClick keeps the ad click IDs the page script posts with the submission
// id, until the queue joins them to the submission's conversions. The route is
// public: it answers 204 whether or not the submission exists (no oracle) and
// 400 only for malformed input.
func postClick(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	if !strings.HasPrefix(strings.ToLower(strings.TrimSpace(r.Headers["content-type"])), "application/json") {
		return invalid("send JSON")
	}
	if r.BodyB64 != "" || len(r.Body) > maxClickBody {
		return invalid("body too large or not text")
	}
	var in struct {
		SubmissionID string         `json:"submission_id"`
		Form         string         `json:"form"`
		IDs          map[string]any `json:"ids"`
	}
	if json.Unmarshal([]byte(r.Body), &in) != nil || !uuidRe.MatchString(in.SubmissionID) {
		return invalid("invalid submission_id")
	}
	if in.Form != "" && !formSlugRe.MatchString(in.Form) {
		return invalid("invalid form")
	}
	ids := cleanIDs(in.IDs)
	for _, k := range idKeys {
		if v, present := in.IDs[k]; present && ids[k] == "" {
			if _, isStr := v.(string); !isStr || v != "" {
				return invalid("invalid " + k)
			}
		}
	}
	if len(ids) == 0 {
		return noContent()
	}
	b, _ := json.Marshal(ids)
	t := now()
	err := dbExec(`INSERT INTO clicks (submission_id, ids, created_at) VALUES ($1, $2::jsonb, $3::timestamptz)
ON CONFLICT (submission_id) DO UPDATE SET ids = clicks.ids || EXCLUDED.ids`, strings.ToLower(in.SubmissionID), string(b), ts(t))
	if err != nil {
		logf("warn", "ads-conversions: could not keep the click IDs")
	}
	if err := dbExec(`DELETE FROM clicks WHERE created_at < $1::timestamptz`, ts(t.Add(-clicksKeep))); err != nil {
		logf("warn", "ads-conversions: could not purge the click IDs")
	}
	return noContent()
}
