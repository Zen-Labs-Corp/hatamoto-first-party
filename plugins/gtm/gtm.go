// Command gtm loads a Google Tag Manager container on every public page. The
// render.head filter publishes the container ID from the plugin settings as a
// meta tag; assets/plugin.js (linked on every page by the core) reads it and
// injects the GTM script.
package main

import (
	"regexp"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

var idRe = regexp.MustCompile(`^GTM-[A-Z0-9]{4,12}$`)

func init() {
	guest.OnFilter(sdk.RenderHead, addContainerMeta)
}

// addContainerMeta never fails the render: an unreadable, empty or invalid
// setting just means no tag.
func addContainerMeta(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		ContainerID string `json:"container_id"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	id := strings.ToUpper(strings.TrimSpace(s.ContainerID))
	if !idRe.MatchString(id) {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "gtm-container", "content": id},
	})
	return c, nil
}
