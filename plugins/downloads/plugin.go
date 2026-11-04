// Command downloads is a Hatamoto plugin. Download counts for files in your media library: tracked download links, daily totals per file and a list of files for the page builder.
//
// Keep the logic in plain Go and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main
