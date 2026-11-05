// Command stock-photos is a Hatamoto plugin. Search Pexels and Openverse from the admin and stage the photos you pick into your media library with alt text and credit.
//
// The plugin is one run ("import") with two stages: fetch searches a provider
// and downloads the photos into the media library, transform stages them as a
// changeset. Keep the logic in plain Go (this package) and the //go:wasmexport
// lines in exports_wasip1.go, so everything but the export builds and tests on
// the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnRunStep(onStep)
}

// onStep routes a run step to its stage; an unknown phase does nothing.
func onStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	switch s.Phase {
	case sdk.RunPhaseFetch:
		return fetchStep(s)
	case sdk.RunPhaseTransform:
		return transformStep(s)
	}
	return sdk.RunStepResult{}, nil
}

func fetchStep(sdk.RunStep) (sdk.RunStepResult, error) {
	return sdk.RunStepResult{}, guest.RunFinish(callHost, nil)
}

func transformStep(sdk.RunStep) (sdk.RunStepResult, error) {
	return sdk.RunStepResult{}, guest.RunFinish(callHost, nil)
}
