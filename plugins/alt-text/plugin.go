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
	"encoding/json"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	defaultLimit = 50
	maxLimit     = 200

	msgNoKey = "Add an API key in the plugin settings."
)

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

// params are the owner's inputs. Blank inputs are absent.
type params struct {
	Limit       int
	OnlyMissing bool
}

// parseParams reads the run inputs: limit is clamped to 1-200 (blank: 50),
// only_missing is true unless given as false.
func parseParams(raw json.RawMessage) params {
	var in struct {
		Limit       float64 `json:"limit"`
		OnlyMissing *bool   `json:"only_missing"`
	}
	if len(raw) > 0 {
		_ = json.Unmarshal(raw, &in)
	}
	p := params{Limit: defaultLimit, OnlyMissing: in.OnlyMissing == nil || *in.OnlyMissing}
	if in.Limit >= 1 {
		p.Limit = int(min(in.Limit, maxLimit))
	}
	return p
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
	return s, nil
}
