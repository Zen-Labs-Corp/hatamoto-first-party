// Command social-feeds is a Hatamoto plugin. It keeps a copy of the latest
// Instagram posts and YouTube videos and serves them to the page builder as
// data sources.
//
// The logic lives in plain Go files and the //go:wasmexport lines in
// exports_wasip1.go, so everything but the exports builds and tests on the
// host.
package main
