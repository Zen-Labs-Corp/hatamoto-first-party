package main

import (
	"encoding/json"
	"os"
	"regexp"
	"slices"
	"testing"
)

type manifest struct {
	Name         string   `json:"name"`
	Version      string   `json:"version"`
	SDK          string   `json:"sdk"`
	Permissions  []string `json:"permissions"`
	AllowedHosts []string `json:"allowed_hosts"`
	Routes       []struct {
		Method string   `json:"method"`
		Path   string   `json:"path"`
		Public bool     `json:"public"`
		Roles  []string `json:"roles"`
	} `json:"routes"`
	UI   map[string]any `json:"ui"`
	Cron []struct {
		Name  string `json:"name"`
		Every string `json:"every"`
	} `json:"cron"`
	Limits struct {
		HookTimeouts map[string]int `json:"hook_timeouts_ms"`
	} `json:"limits"`
	Schema struct {
		Properties map[string]map[string]any `json:"properties"`
	} `json:"settings_schema"`
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

func TestManifestPermissionsAndHosts(t *testing.T) {
	m := loadManifest(t)
	if m.Name != "form-alerts" || m.Version != "0.1.0" || m.SDK != "^0.8" {
		t.Fatalf("identity %q %q %q", m.Name, m.Version, m.SDK)
	}
	got := slices.Clone(m.Permissions)
	slices.Sort(got)
	if want := []string{"db", "forms.read", "http.fetch", "log", "settings"}; !slices.Equal(got, want) {
		t.Fatalf("permissions %v", m.Permissions)
	}
	hosts := slices.Clone(m.AllowedHosts)
	slices.Sort(hosts)
	want := []string{"*.environment.api.powerplatform.com", "*.logic.azure.com", "api.telegram.org", "chat.googleapis.com", "hooks.slack.com"}
	if !slices.Equal(hosts, want) {
		t.Fatalf("allowed_hosts %v", m.AllowedHosts)
	}
	if slices.Contains(m.Permissions, "http.fetch.any") {
		t.Fatal("http.fetch.any is not allowed")
	}
}

func TestManifestRoutesAreOwnerOnly(t *testing.T) {
	m := loadManifest(t)
	seen := map[string]bool{}
	for _, r := range m.Routes {
		if r.Public || len(r.Roles) != 0 {
			t.Fatalf("route %s %s must be owner only", r.Method, r.Path)
		}
		seen[r.Method+" "+r.Path] = true
	}
	if len(m.Routes) != 2 || !seen["GET /log"] || !seen["POST /test"] {
		t.Fatalf("routes %+v", m.Routes)
	}
	if _, ok := m.UI["roles"]; ok {
		t.Fatal("ui.roles must be absent (owner only)")
	}
}

func TestManifestCronAndHookTimeouts(t *testing.T) {
	m := loadManifest(t)
	if len(m.Cron) != 2 || m.Cron[0].Name != "deliver" || m.Cron[0].Every != "1m" || m.Cron[1].Name != "retry" || m.Cron[1].Every != "5m" {
		t.Fatalf("cron %+v", m.Cron)
	}
	want := map[string]int{"form.submitted": 9000, "on_cron_deliver": 10000, "on_cron_retry": 10000}
	if len(m.Limits.HookTimeouts) != len(want) {
		t.Fatalf("hook timeouts %v", m.Limits.HookTimeouts)
	}
	for k, v := range want {
		if m.Limits.HookTimeouts[k] != v {
			t.Fatalf("hook timeouts %v", m.Limits.HookTimeouts)
		}
	}
}

func TestManifestSecretsAreWriteOnly(t *testing.T) {
	p := loadManifest(t).Schema.Properties
	for _, k := range []string{"slack_webhook_url", "teams_workflow_url", "gchat_webhook_url", "telegram_bot_token"} {
		if p[k]["writeOnly"] != true {
			t.Fatalf("%s must be writeOnly", k)
		}
	}
	if p["include_values"]["default"] != true || p["max_value_length"]["default"] != float64(500) {
		t.Fatal("include_values defaults to true, max_value_length to 500")
	}
	for k, v := range p {
		if d, _ := v["description"].(string); d == "" {
			t.Fatalf("%s has no description", k)
		}
	}
}

func TestManifestPatterns(t *testing.T) {
	p := loadManifest(t).Schema.Properties
	const (
		teamsOld = "https://prod-12.westus.logic.azure.com:443/workflows/x/triggers/manual/paths/invoke?sig=1"
		teamsNew = "https://default1.56.environment.api.powerplatform.com:443/powerautomate/automations/direct/workflows/x?sig=1"
		gchat    = "https://chat.googleapis.com/v1/spaces/AAA/messages?key=k&token=t"
		slack    = "https://hooks.slack.com/services/T0/B0/XXXX"
		token    = "123456:abcdefghijklmnopqrstuvwxyz"
	)
	cases := []struct {
		prop   string
		accept []string
		reject []string
	}{
		{"slack_webhook_url", []string{"", slack}, []string{"http://hooks.slack.com/x", "https://hooks.slack.com.evil.com/x", "https://user@hooks.slack.com/x", "https://evil.com/?x=hooks.slack.com/"}},
		{"teams_workflow_url", []string{"", teamsOld, teamsNew}, []string{"https://evil.com/?x=.logic.azure.com", "https://example.com:8443/x", "https://user@a.logic.azure.com/x", "http://a.logic.azure.com/x", "https://a.logic.azure.com:8443/x", "https://logic.azure.com/x"}},
		{"gchat_webhook_url", []string{"", gchat}, []string{"https://chat.googleapis.com.evil.com/v1/spaces/A/messages?k=1", "http://chat.googleapis.com/v1/spaces/A/messages?k=1", "https://chat.googleapis.com/v1/spaces/A/messages"}},
		{"telegram_bot_token", []string{"", token}, []string{"abc", "123:short"}},
		{"telegram_chat_id", []string{"", "-1001234", "12345", "@mychannel"}, []string{"@x", "chat", "12 34", "@my channel"}},
		{"admin_url", []string{"", "https://example.com/admin", "https://example.com"}, []string{"http://example.com/admin", "ftp://example.com/admin", "https://user@example.com/admin", "https://example.com/admin?x=1"}},
	}
	for _, c := range cases {
		pat, _ := p[c.prop]["pattern"].(string)
		re, err := regexp.Compile(pat)
		if err != nil {
			t.Fatalf("%s: pattern does not compile: %v", c.prop, err)
		}
		for _, s := range c.accept {
			if !re.MatchString(s) {
				t.Errorf("%s rejects %q", c.prop, s)
			}
		}
		for _, s := range c.reject {
			if re.MatchString(s) {
				t.Errorf("%s accepts %q", c.prop, s)
			}
		}
	}
}
