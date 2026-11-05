// Command auto-translate is a Hatamoto plugin. Machine translation with DeepL or Google Cloud Translation: stages translated copies of your pages and posts for review, linked as translations.
//
// The plugin has one run, "translate": a fetch phase that sends published
// content to the provider and a transform phase that stages the translated
// copies in a changeset. Keep the logic in plain Go (this file and its
// siblings) and the //go:wasmexport line in exports_wasip1.go, so everything
// but the export builds and tests on the host.
package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnRunStep(onStep)
}

// onStep dispatches by phase. Any error returned fails the run (host contract),
// so every provider or host problem is turned into run.fail plus a nil error
// by the phase code.
func onStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	switch s.Phase {
	case sdk.RunPhaseFetch:
		return fetchStep(s)
	case sdk.RunPhaseTransform:
		return transformStep(s)
	}
	return sdk.RunStepResult{}, nil
}
