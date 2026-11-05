package main

import (
	"encoding/json"
	"os"
	"reflect"
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

func TestDeclaredHostsMatchManifest(t *testing.T) {
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Hosts []string `json:"allowed_hosts"`
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(m.Hosts, declaredHosts) {
		t.Fatalf("plugin.json allowed_hosts %v != declaredHosts %v", m.Hosts, declaredHosts)
	}
}
