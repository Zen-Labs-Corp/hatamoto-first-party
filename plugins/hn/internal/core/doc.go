// Package core is the seam every phase of the hn plugin builds on: the host
// call seam and database helpers, the registry the phases register handlers
// into, the viewer and request helpers, settings, and the HN title, URL and
// username rules.
//
// It builds on the host and as wasm, imports no phase package and never the
// guest package: the root of the plugin sets Call.
package core
