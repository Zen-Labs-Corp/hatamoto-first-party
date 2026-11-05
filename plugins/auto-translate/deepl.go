package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// deepl is the DeepL API client. The key travels in a header only.
type deepl struct{ key, host, formality string }

type deeplRequest struct {
	Text        []string `json:"text"`
	TargetLang  string   `json:"target_lang"`
	SourceLang  string   `json:"source_lang,omitempty"`
	TagHandling string   `json:"tag_handling"`
	Formality   string   `json:"formality,omitempty"`
}

// translate sends the texts in one request and returns the translations in
// the same order. There is no retry.
func (d deepl) translate(texts []string, target, source string) ([]string, error) {
	req := deeplRequest{Text: texts, TargetLang: target, SourceLang: source, TagHandling: "html"}
	if d.formality != "" && d.formality != "default" && deeplFormalitySupported(target) {
		req.Formality = d.formality
	}
	body, err := json.Marshal(req)
	if err != nil {
		return nil, errors.New("DeepL request could not be built")
	}
	res, err := httpFetch(fetchReq{
		Method: "POST",
		URL:    "https://" + d.host + "/v2/translate",
		Headers: map[string]string{
			"Authorization": "DeepL-Auth-Key " + d.key,
			"Content-Type":  "application/json",
		},
		Body: string(body),
	})
	if err != nil {
		return nil, errors.New(redact(err.Error(), d.key))
	}
	if res.Status != 200 {
		return nil, d.statusError(res)
	}
	var out struct {
		Translations []struct {
			Text string `json:"text"`
		} `json:"translations"`
	}
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		return nil, errors.New("DeepL returned an unreadable response")
	}
	if len(out.Translations) != len(texts) {
		return nil, fmt.Errorf("DeepL returned %d translations for %d texts", len(out.Translations), len(texts))
	}
	r := make([]string, len(out.Translations))
	for i, t := range out.Translations {
		r[i] = t.Text
	}
	return r, nil
}

// statusError maps a DeepL error status to one short line without the key.
func (d deepl) statusError(res fetched) error {
	switch s := res.Status; {
	case s == 403:
		return errors.New("DeepL rejected the key (check the key and whether it is a Free or Pro key)")
	case s == 456:
		return errors.New("DeepL quota exceeded")
	case s == 429 || s == 529:
		return errors.New("DeepL is rate limiting requests, try again later")
	case s == 413:
		return errors.New("request too large")
	case s == 400:
		var e struct {
			Message string `json:"message"`
		}
		_ = json.Unmarshal([]byte(res.Body), &e)
		return errors.New("DeepL refused the request: " + shortLine(redact(e.Message, d.key), 200))
	case s >= 500:
		return errors.New("DeepL is temporarily unavailable")
	default:
		return fmt.Errorf("DeepL error %d", s)
	}
}

// shortLine collapses whitespace to single spaces and cuts to max runes. Call
// redact first.
func shortLine(s string, max int) string {
	s = strings.Join(strings.Fields(s), " ")
	if r := []rune(s); len(r) > max {
		s = string(r[:max]) + "..."
	}
	return s
}
