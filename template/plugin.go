// Command __NAME__ is a Hatamoto plugin. __DESCRIPTION__
//
// Starting point from the first-party template: a render.head filter that
// reads one setting and adds a meta tag. Replace it with the real hooks; keep
// the logic in plain Go (this file) and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main

import (
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnFilter(sdk.RenderHead, addMeta)
}

// addMeta never fails the render: an unreadable or empty setting adds nothing.
func addMeta(c sdk.HeadContext) (sdk.HeadContext, error) {
	var s struct {
		Greeting string `json:"greeting"`
	}
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return c, nil
	}
	g := strings.TrimSpace(s.Greeting)
	if g == "" {
		return c, nil
	}
	c.Tags = append(c.Tags, sdk.HeadTag{
		Kind:  "meta",
		Attrs: map[string]string{"name": "__NAME__", "content": g},
	})
	return c, nil
}
