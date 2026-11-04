package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"
)

func TestManifestPermissions(t *testing.T) {
	b, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Permissions  []string `json:"permissions"`
		AllowedHosts []string `json:"allowed_hosts"`
	}
	if err := json.Unmarshal(b, &m); err != nil {
		t.Fatalf("plugin.json does not parse: %v", err)
	}
	want := []string{"db", "entities.read", "http.fetch", "log", "settings"}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if !slices.Equal(got, want) {
		t.Fatalf("permissions = %v, want %v", got, want)
	}
	if len(m.AllowedHosts) != 2 {
		t.Fatalf("allowed_hosts = %v", m.AllowedHosts)
	}
}

// jsonInto decodes raw into out, as a host reply would be.
func jsonInto(raw string, out any) error {
	if out == nil {
		return nil
	}
	return json.Unmarshal([]byte(raw), out)
}
