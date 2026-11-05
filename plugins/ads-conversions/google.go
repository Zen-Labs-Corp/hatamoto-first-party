package main

import (
	"encoding/json"
	"time"
)

const googleURL = "https://datamanager.googleapis.com/v1/events:ingest"

// googleAccount is a Data Manager account reference.
func googleAccount(id string) map[string]any {
	return map[string]any{"accountType": "GOOGLE_ADS", "accountId": id}
}

// buildGoogle is the Data Manager events:ingest request for one conversion.
// transactionId is the submission id, so Google deduplicates it with the
// browser tag. With validateOnly Google checks the request and records
// nothing. The access token travels only in the header.
func buildGoogle(s settings, j job, token string, validateOnly bool) request {
	t := j.SubmittedAt
	if t.IsZero() {
		t = now()
	}
	dest := map[string]any{
		"operatingAccount":     googleAccount(s.Google.CustomerID),
		"productDestinationId": s.Google.ConversionActionID,
	}
	if s.Google.LoginCustomerID != "" {
		dest["loginAccount"] = googleAccount(s.Google.LoginCustomerID)
	}
	ev := map[string]any{
		"transactionId":  j.SubmissionID,
		"eventTimestamp": t.UTC().Format(time.RFC3339),
		"eventSource":    "WEB",
	}
	var ident []any
	if j.EmGoogleHash != "" {
		ident = append(ident, map[string]any{"emailAddress": j.EmGoogleHash})
	}
	if j.PhHash != "" {
		ident = append(ident, map[string]any{"phoneNumber": j.PhHash})
	}
	if len(ident) > 0 {
		ev["userData"] = map[string]any{"userIdentifiers": ident}
	}
	// One click ID is enough; gclid is the most specific.
	for _, k := range []string{"gclid", "gbraid", "wbraid"} {
		if v := j.IDs[k]; v != "" {
			ev["adIdentifiers"] = map[string]any{k: v}
			break
		}
	}
	if s.Value != nil {
		ev["conversionValue"] = *s.Value
	}
	if s.Currency != "" {
		ev["currency"] = s.Currency
	}
	body := map[string]any{
		"destinations": []any{dest},
		"encoding":     "HEX",
		"events":       []any{ev},
	}
	if j.Consent {
		body["consent"] = map[string]any{"adUserData": "CONSENT_GRANTED"}
	}
	if validateOnly {
		body["validateOnly"] = true
	}
	b, _ := json.Marshal(body)
	return request{
		Method:  "POST",
		URL:     googleURL,
		Headers: map[string]string{"Content-Type": "application/json", "Authorization": "Bearer " + token},
		Body:    b,
	}
}

// hasIdentifierGoogle reports whether Google has something to match on.
func hasIdentifierGoogle(j job) bool {
	return j.EmGoogleHash != "" || j.PhHash != "" || j.IDs["gclid"] != "" || j.IDs["gbraid"] != "" || j.IDs["wbraid"] != ""
}

// classifyGoogle reads Data Manager's answer: 200 is accepted (Google then
// processes it asynchronously), 401 means the access token was refused, 429
// and 5xx are retried, any other status is final and carries Google's message.
func classifyGoogle(status int, body []byte) outcome {
	o := classifyHTTP(status, false)
	if o.Kind != kindSent {
		o.Message = cut(apiErrorMessage(body), maxMessage)
	}
	return o
}

// apiErrorMessage reads the message of Google's {"error":{"message":..}} or
// LinkedIn's {"message":..} error body; "" when there is none.
func apiErrorMessage(body []byte) string {
	var e struct {
		Message string `json:"message"`
		Error   struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &e) != nil {
		return ""
	}
	if e.Error.Message != "" {
		return e.Error.Message
	}
	return e.Message
}
