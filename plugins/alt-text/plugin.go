// Command alt-text is a Hatamoto plugin. Suggests alt text for images in your media library that have none, using a vision model you choose, and stages each one for your review.
//
// The "suggest" run has two phases. The fetch phase (fetch.go) walks the
// images, asks the chosen provider for one description per step and keeps the
// answers in the run buffer. The transform phase (transform.go) stages them as
// changeset.media.alt calls. All logic is plain Go here; the //go:wasmexport
// line sits in exports_wasip1.go, so everything but the export builds and
// tests on the host.
package main

import (
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	defaultLimit = 50
	maxLimit     = 200

	msgNoKey = "Add an API key in the plugin settings."
)

// clampLimit returns the number of images to check: blank or invalid gives
// the default, anything outside 1-200 is cut to it.
func clampLimit(v float64) float64 {
	if v < 1 {
		return defaultLimit
	}
	return min(float64(int(v)), maxLimit)
}

func init() {
	guest.OnRunStep(onStep)
}

// onStep dispatches one step of the run by phase.
func onStep(st sdk.RunStep) (sdk.RunStepResult, error) {
	switch st.Phase {
	case sdk.RunPhaseFetch:
		return sdk.RunStepResult{}, fetchStep(st)
	case sdk.RunPhaseTransform:
		return sdk.RunStepResult{}, transformStep(st)
	}
	return sdk.RunStepResult{}, nil
}

// loadSettings reads and normalises the plugin settings.
func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return s, err
	}
	s.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	if s.Provider == "" {
		s.Provider = providerOpenAI
	}
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.SiteURL = strings.TrimRight(strings.TrimSpace(s.SiteURL), "/")
	s.MaxLength = clampMax(s.MaxLength)
	s.Limit = clampLimit(s.Limit)
	return s, nil
}
