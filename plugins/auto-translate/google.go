package main

import (
	"encoding/json"
	"errors"
	"fmt"
)

const googleURL = "https://" + googleHost + "/language/translate/v2"

// google is the Google Cloud Translation (v2) client. The key travels in a
// header only, never in the URL, because the host logs URLs.
type google struct{ key string }

type googleRequest struct {
	Q      []string `json:"q"`
	Target string   `json:"target"`
	Source string   `json:"source,omitempty"`
	Format string   `json:"format"`
}

// translate sends the texts in one request and returns the translations in
// the same order, as the provider returned them (markup kept as is). There is
// no retry.
func (g google) translate(texts []string, target, source string) ([]string, error) {
	body, err := json.Marshal(googleRequest{Q: texts, Target: target, Source: source, Format: "html"})
	if err != nil {
		return nil, errors.New("Google request could not be built")
	}
	res, err := httpFetch(fetchReq{
		Method: "POST",
		URL:    googleURL,
		Headers: map[string]string{
			"X-Goog-Api-Key": g.key,
			"Content-Type":   "application/json",
		},
		Body: string(body),
	})
	if errors.Is(err, errBudget) {
		return nil, err // the step resumes, see fetchStep
	}
	if err != nil {
		return nil, errors.New(redact(err.Error(), g.key))
	}
	if res.Status != 200 {
		return nil, g.statusError(res)
	}
	var out struct {
		Data struct {
			Translations []struct {
				TranslatedText string `json:"translatedText"`
			} `json:"translations"`
		} `json:"data"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		return nil, errors.New("Google returned an unreadable response")
	}
	if len(out.Data.Translations) != len(texts) {
		return nil, fmt.Errorf("Google returned %d translations for %d texts", len(out.Data.Translations), len(texts))
	}
	r := make([]string, len(texts))
	for i, t := range out.Data.Translations {
		r[i] = t.TranslatedText
	}
	return r, nil
}

// statusError maps a Google error status to one short line without the key.
func (g google) statusError(res fetched) error {
	var e struct {
		Error struct {
			Message string `json:"message"`
			Errors  []struct {
				Reason string `json:"reason"`
			} `json:"errors"`
		} `json:"error"`
	}
	_ = json.Unmarshal([]byte(res.Body), &e)
	msg := shortLine(redact(e.Error.Message, g.key), 200)
	switch s := res.Status; {
	case s == 400:
		for _, x := range e.Error.Errors {
			if x.Reason == "keyInvalid" {
				return errors.New("Google rejected the API key")
			}
		}
		return errors.New("Google refused the request: " + msg)
	case s == 403:
		return errors.New("Google Translation is not enabled for this key or the quota/billing is exhausted: " + msg)
	case s == 429:
		return errors.New("Google is rate limiting requests, try again later")
	case s >= 500:
		return errors.New("Google Translation is temporarily unavailable")
	default:
		return fmt.Errorf("Google error %d", s)
	}
}
