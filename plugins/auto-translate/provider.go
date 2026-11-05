package main

import (
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	providerDeepL  = "deepl"
	providerGoogle = "google"

	deeplProHost  = "api.deepl.com"
	deeplFreeHost = "api-free.deepl.com"
	googleHost    = "translation.googleapis.com"

	// maxCallTimeout caps one provider request; minCall is the least time
	// left in the step for which a request is still started.
	maxCallTimeout = 7 * time.Second
	minCall        = 1500 * time.Millisecond
)

// glossaryEntry is one glossary term: kept as written when Target is empty,
// replaced by Target otherwise.
type glossaryEntry struct {
	Source string `json:"source"`
	Target string `json:"target"`
}

type settings struct {
	Provider  string          `json:"provider"`
	APIKey    string          `json:"api_key"`
	Formality string          `json:"formality"`
	Glossary  []glossaryEntry `json:"glossary"`
}

// loadSettings reads the plugin settings and normalizes them. The provider
// defaults to DeepL and glossary entries without a source are dropped.
func loadSettings() (settings, error) {
	var raw settings
	if err := callHost("settings.get", struct{}{}, &raw); err != nil {
		return settings{}, errors.New("the plugin settings could not be read")
	}
	s := settings{
		Provider:  strings.ToLower(strings.TrimSpace(raw.Provider)),
		APIKey:    strings.TrimSpace(raw.APIKey),
		Formality: strings.ToLower(strings.TrimSpace(raw.Formality)),
	}
	if s.Provider == "" {
		s.Provider = providerDeepL
	}
	if s.Formality == "" {
		s.Formality = "default"
	}
	for _, g := range raw.Glossary {
		g.Source = strings.TrimSpace(g.Source)
		g.Target = strings.TrimSpace(g.Target)
		if g.Source != "" {
			s.Glossary = append(s.Glossary, g)
		}
	}
	return s, nil
}

// configured reports a settings problem in words an owner can act on. The
// text never contains the key.
func (s settings) configured() error {
	if s.Provider != providerDeepL && s.Provider != providerGoogle {
		return fmt.Errorf("unknown provider %q (choose deepl or google in the plugin settings)", s.Provider)
	}
	if s.APIKey == "" {
		return errors.New("no API key is set in the plugin settings")
	}
	return nil
}

type fetchReq struct {
	Method    string            `json:"method"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	Body      string            `json:"body,omitempty"`
	TimeoutMS int               `json:"timeout_ms"`
}

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// budgetEnd is the time by which the running step must be done; the fetch
// phase sets it to the step start plus 8.5 s. The zero value means no budget.
var budgetEnd time.Time

var errBudget = errors.New("step time budget used up")

// httpFetch calls the host's http.fetch once, with no retry. Credentials go
// in headers only. No returned error carries the URL or a header.
func httpFetch(r fetchReq) (fetched, error) {
	timeout := maxCallTimeout
	if !budgetEnd.IsZero() {
		left := time.Until(budgetEnd)
		if left < minCall {
			return fetched{}, errBudget
		}
		timeout = min(timeout, left)
	}
	r.TimeoutMS = int(timeout / time.Millisecond)
	var res fetched
	if err := callHost("http.fetch", r, &res); err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) {
			switch he.Code {
			case sdk.ErrTimeout:
				return fetched{}, errors.New("provider timed out")
			case sdk.ErrBlocked:
				return fetched{}, errors.New("host not allowed")
			}
			return fetched{}, fmt.Errorf("provider request failed (%s)", he.Code)
		}
		return fetched{}, errors.New("provider request failed")
	}
	if res.Truncated {
		return fetched{}, errors.New("response too large")
	}
	return res, nil
}

// redact hides secret, plain and URL-escaped, in msg. Call it before any
// truncation, so a key cut in half by a length limit cannot survive.
func redact(msg, secret string) string {
	if secret == "" {
		return msg
	}
	msg = strings.ReplaceAll(msg, secret, "***")
	if esc := url.QueryEscape(secret); esc != secret {
		msg = strings.ReplaceAll(msg, esc, "***")
	}
	return msg
}

// providerHost returns the API host the settings call and checks that the
// owner confirmed it when the run started.
func providerHost(s settings, hosts []string) (string, error) {
	var h string
	switch s.Provider {
	case providerDeepL:
		h = deeplProHost
		if strings.HasSuffix(s.APIKey, ":fx") {
			h = deeplFreeHost
		}
	case providerGoogle:
		h = googleHost
	default:
		return "", fmt.Errorf("unknown provider %q", s.Provider)
	}
	if !slices.ContainsFunc(hosts, func(x string) bool { return strings.EqualFold(x, h) }) {
		return "", fmt.Errorf("the host %s was not confirmed when the run started", h)
	}
	return h, nil
}

// translator translates HTML fragments in order. The batch has already been
// sized to fit one provider request.
type translator interface {
	translate(texts []string, target, source string) ([]string, error)
}
