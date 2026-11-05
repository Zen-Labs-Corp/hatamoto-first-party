package main

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func TestFetchStepWithoutQueryFails(t *testing.T) {
	var calls []string
	callHost = func(fn string, _, _ any) error {
		calls = append(calls, fn)
		return nil
	}
	in, _ := json.Marshal(sdk.RunStep{RunID: "r", Phase: sdk.RunPhaseFetch})
	if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
		t.Fatalf("step returned an error: %v", err)
	}
	if len(calls) != 2 || calls[0] != "run.info" || calls[1] != "run.fail" {
		t.Fatalf("host calls = %v", calls)
	}
}
