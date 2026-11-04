package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

func init() {
	guest.OnAction(sdk.EntityPublished, onPublished)
}

// onPublished re-reads the links of a page the moment it is published: its
// page rows are replaced (removed links drop off, new ones are queued as
// pending and checked by the next cron run), and broken internal links are
// queued again. It uses no network. Any failure
// is logged and swallowed, so publishing is never affected.
func onPublished(ev sdk.EntityEvent) error {
	var e entityWire
	if err := callHost(sdk.FnEntitiesGet, map[string]string{"id": ev.ID}, &e); err != nil {
		logf("warn", "published page not readable: %v", err)
		return nil
	}
	s := loadSettings()
	if err := indexEntity(e, s.Exclude, siteOrigin(e.URL, e.Path)); err != nil {
		logf("warn", "storing links of a published page: %v", err)
	}
	// The page may be the target a broken internal link was missing.
	if err := recheckBrokenInternal(); err != nil {
		logf("warn", "queueing broken internal links: %v", err)
	}
	return nil
}
