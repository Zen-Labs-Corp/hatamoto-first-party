// Command google-analytics adds the Google tag (GA4) to every public page. The
// render.head filter publishes the measurement ID from the plugin settings as a
// meta tag; assets/plugin.js (linked on every page by the core) reads it and
// injects gtag.js.
package main

import (
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

var idRe = regexp.MustCompile(`^(G|GT)-[A-Z0-9]{4,16}$`)

func init() {
	guest.OnFilter(sdk.RenderHead, addMeasurementMeta)
}

// addMeasurementMeta never fails the render: an unreadable, empty or invalid
// setting just means no tag.
func addMeasurementMeta(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		MeasurementID string `json:"measurement_id"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	id := strings.ToUpper(strings.TrimSpace(s.MeasurementID))
	if !idRe.MatchString(id) {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "google-analytics-id", "content": id},
	})
	return c, nil
}
