package main

import "strings"

// settings is the plugin's settings object (plugin.json settings_schema).
type settings struct {
	Provider        string   `json:"provider"`
	ZoneID          string   `json:"zone_id"`
	PullZoneID      string   `json:"pull_zone_id"`
	ServiceID       string   `json:"service_id"`
	APIToken        string   `json:"api_token"`
	AlsoPurge       []string `json:"also_purge"`
	PurgeEverything bool     `json:"purge_everything_on_change"`
	SiteURL         string   `json:"site_url"`
}

const (
	providerCloudflare = "cloudflare"
	providerBunny      = "bunny"
	providerFastly     = "fastly"
)

func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return s, err
	}
	s.Provider = strings.ToLower(strings.TrimSpace(s.Provider))
	s.ZoneID = strings.TrimSpace(s.ZoneID)
	s.PullZoneID = strings.TrimSpace(s.PullZoneID)
	s.ServiceID = strings.TrimSpace(s.ServiceID)
	s.APIToken = strings.TrimSpace(s.APIToken)
	s.SiteURL = strings.TrimRight(strings.TrimSpace(s.SiteURL), "/")
	var paths []string
	for _, p := range s.AlsoPurge {
		if p = strings.TrimSpace(p); p != "" {
			paths = append(paths, p)
		}
	}
	s.AlsoPurge = paths
	return s, nil
}

// target is the zone, pull zone or service id for the chosen provider.
func (s settings) target() string {
	switch s.Provider {
	case providerCloudflare:
		return s.ZoneID
	case providerBunny:
		return s.PullZoneID
	case providerFastly:
		return s.ServiceID
	}
	return ""
}

// configured reports whether a purge can be attempted at all.
func (s settings) configured() bool {
	return s.target() != "" && s.APIToken != ""
}
