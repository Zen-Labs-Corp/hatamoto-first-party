// Command feed-reader is a Hatamoto plugin. It pulls items from RSS and Atom
// feeds the owner chooses on a schedule and answers the page-builder data
// sources, linking back to each source.
//
// Keep the logic in plain Go and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main
