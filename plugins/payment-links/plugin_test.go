package main

import (
	"encoding/json"
	"os"
	"slices"
	"testing"

	"hatamoto.dev/sdk"
)

type manifest struct {
	Name         string   `json:"name"`
	Permissions  []string `json:"permissions"`
	AllowedHosts []string `json:"allowed_hosts"`
	Routes       []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	UI map[string]any `json:"ui"`
}

func loadManifest(t *testing.T) manifest {
	t.Helper()
	raw, err := os.ReadFile("plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m manifest
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	return m
}

func TestManifestPermissionsHostsAndRoutes(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "payment-links" {
		t.Fatalf("name %q", m.Name)
	}
	perms := slices.Clone(m.Permissions)
	slices.Sort(perms)
	want := []string{"db", "http.fetch", "log", "mail.send", "settings"}
	if !slices.Equal(perms, want) {
		t.Fatalf("permissions %v, want %v", perms, want)
	}
	if !slices.Equal(m.AllowedHosts, []string{"api.stripe.com"}) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
	var public []string
	for _, r := range m.Routes {
		if r.Public {
			public = append(public, r.Method+" "+r.Path)
		}
		if len(r.Roles) != 0 {
			t.Errorf("route %s %s sets roles", r.Method, r.Path)
		}
	}
	if !slices.Equal(public, []string{"POST /webhook"}) {
		t.Fatalf("public routes %v", public)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
	if len(m.Cron) != 1 || m.Cron[0].Name != "prune" {
		t.Fatalf("cron %+v", m.Cron)
	}
}

func TestUnknownRouteIs404(t *testing.T) {
	res, err := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"})
	if err != nil || res.Status != 404 {
		t.Fatalf("%v %v", res, err)
	}
}
