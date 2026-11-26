package main

import "slices"

// scheduledHosts are the hosts a scheduled run may read: the plugin.json
// allowed_hosts without the geocoder and without "*". A run started by the
// cron job never gets the "*" grant, so any other address is imported with
// Run now only.
var scheduledHosts = []string{
	"calendar.google.com",
	"outlook.office365.com",
	"outlook.office.com",
	"outlook.live.com",
	"www.meetup.com",
	"www.eventbrite.com",
	"www.eventbriteapi.com",
}

// hostListed reports whether the address is on a host a scheduled run can read.
func hostListed(rawURL string) bool {
	u, err := normaliseFeedURL(rawURL)
	if err != nil {
		return false
	}
	return slices.Contains(scheduledHosts, hostOf(u))
}
