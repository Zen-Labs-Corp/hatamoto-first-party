// Command reviews is a Hatamoto plugin. It keeps a daily-refreshed copy of
// the Google and Trustpilot reviews of one business and shows it through the
// ext.reviews.latest page-builder source, the plugin_reviews_* template
// functions and the ready-made "reviews" section.
//
// The logic is plain Go in this package; the //go:wasmexport lines live in
// exports_wasip1.go, so everything but the exports builds and tests on the
// host. Every hook, function and route fails open: no keys or a host error
// gives empty data, never an error.
package main

import "time"

// now is the clock; tests replace it.
var now = time.Now
