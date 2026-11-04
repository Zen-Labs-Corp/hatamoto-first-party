// Command link-checker is a Hatamoto plugin. Finds broken outbound and
// internal links in your published content on a schedule and lists each one
// with the page it is on and a link to fix it.
//
// The logic lives in plain Go files (extract, internal, outbound, scan,
// published, routes, store); the //go:wasmexport lines are in
// exports_wasip1.go, so everything but the exports builds and tests on the host.
package main
