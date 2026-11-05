package main

import (
	"encoding/json"
	"slices"
	"strings"
)

type config struct {
	Schemas     []string
	AllowChange bool
}

func defaultConfig() config { return config{Schemas: []string{"post"}} }

// loadConfig reads the settings. Any error gives the defaults; one bad field
// does not drop the others.
func loadConfig() config {
	var raw struct {
		Schemas     json.RawMessage `json:"schemas"`
		AllowChange json.RawMessage `json:"allow_change"`
	}
	c := defaultConfig()
	if err := callHost("settings.get", struct{}{}, &raw); err != nil {
		return c
	}
	var schemas []string
	if json.Unmarshal(raw.Schemas, &schemas) == nil {
		c.Schemas = nil
		for _, s := range schemas {
			if s = strings.TrimSpace(s); s != "" {
				c.Schemas = append(c.Schemas, s)
			}
		}
	}
	var allow bool
	if json.Unmarshal(raw.AllowChange, &allow) == nil {
		c.AllowChange = allow
	}
	return c
}

// allows reports whether entities of this schema can be rated.
func (c config) allows(schema string) bool {
	return schema != "" && slices.Contains(c.Schemas, schema)
}
