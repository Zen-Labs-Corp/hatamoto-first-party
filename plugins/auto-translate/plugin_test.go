package main

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// TestUnknownPhaseMakesNoHostCall drives a phase the plugin does not know
// through the registered handler: it returns cleanly and touches no host
// function.
func TestUnknownPhaseMakesNoHostCall(t *testing.T) {
	callHost = func(fn string, _, _ any) error {
		t.Fatalf("unexpected host call %q", fn)
		return nil
	}
	for _, phase := range []string{"other", ""} {
		in, _ := json.Marshal(sdk.RunStep{RunID: "r1", Phase: phase})
		if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
			t.Fatalf("phase %q: step returned an error: %v", phase, err)
		}
	}
}
