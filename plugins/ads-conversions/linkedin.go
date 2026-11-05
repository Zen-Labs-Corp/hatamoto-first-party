package main

import (
	"encoding/json"
	"strconv"
)

const linkedInAPI = "https://api.linkedin.com/rest/"

func linkedInHeaders(s settings, token string) map[string]string {
	return map[string]string{
		"Content-Type":              "application/json",
		"Authorization":             "Bearer " + token,
		"LinkedIn-Version":          s.LinkedIn.Version,
		"X-Restli-Protocol-Version": "2.0.0",
	}
}

// buildLinkedIn is the Conversions API request for one conversion. eventId is
// the submission id, so LinkedIn deduplicates it with the browser tag. No
// phone and no names are sent: LinkedIn matches on the hashed email and the
// first-party tracking ID. The token travels only in the header.
func buildLinkedIn(s settings, j job, token string) request {
	t := j.SubmittedAt
	if t.IsZero() {
		t = now()
	}
	var ids []any
	if j.EmHash != "" {
		ids = append(ids, map[string]any{"idType": "SHA256_EMAIL", "idValue": j.EmHash})
	}
	if v := j.IDs["li_fat_id"]; v != "" {
		ids = append(ids, map[string]any{"idType": "LINKEDIN_FIRST_PARTY_ADS_TRACKING_UUID", "idValue": v})
	}
	body := map[string]any{
		"conversion":           "urn:lla:llaPartnerConversion:" + s.LinkedIn.RuleID,
		"conversionHappenedAt": t.UnixMilli(),
		"user":                 map[string]any{"userIds": ids},
		"eventId":              j.SubmissionID,
	}
	if s.Value != nil && s.Currency != "" {
		body["conversionValue"] = map[string]any{
			"currencyCode": s.Currency,
			"amount":       strconv.FormatFloat(*s.Value, 'f', -1, 64),
		}
	}
	b, _ := json.Marshal(body)
	return request{Method: "POST", URL: linkedInAPI + "conversionEvents", Headers: linkedInHeaders(s, token), Body: b}
}

// buildLinkedInCheck reads the conversion rule, which proves the token, the
// version and the ids work without sending a conversion (LinkedIn has no test
// mode).
func buildLinkedInCheck(s settings, token string) request {
	return request{
		Method:  "GET",
		URL:     linkedInAPI + "conversions/" + s.LinkedIn.RuleID + "?account=urn%3Ali%3AsponsoredAccount%3A" + s.LinkedIn.AdAccountID,
		Headers: linkedInHeaders(s, token),
	}
}

// hasIdentifierLinkedIn reports whether LinkedIn has something to match on.
func hasIdentifierLinkedIn(j job) bool {
	return j.EmHash != "" || j.IDs["li_fat_id"] != ""
}

// classifyLinkedIn reads LinkedIn's answer: 201 is accepted, 401 means the
// access token was refused, 429 and 5xx are retried, any other status is final
// and carries LinkedIn's message.
func classifyLinkedIn(status int, body []byte) outcome {
	o := classifyHTTP(status, false)
	if o.Kind != kindSent {
		o.Message = cut(apiErrorMessage(body), maxMessage)
	}
	return o
}
