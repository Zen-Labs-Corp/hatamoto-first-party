package main

import (
	"encoding/json"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func TestFetchStepFinishes(t *testing.T) {
	var calls []string
	callHost = func(fn string, _, _ any) error {
		calls = append(calls, fn)
		return nil
	}
	in, _ := json.Marshal(sdk.RunStep{RunID: "r", Phase: sdk.RunPhaseFetch})
	if _, err := guest.Handle(sdk.RunStepExport, in); err != nil {
		t.Fatalf("step returned an error: %v", err)
	}
	if len(calls) != 1 || calls[0] != "run.finish" {
		t.Fatalf("host calls = %v", calls)
	}
}
