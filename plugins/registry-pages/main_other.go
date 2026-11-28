//go:build !wasip1

package main

// main exists so the package vets and tests on the host; the plugin itself is
// only ever built for wasip1.
func main() {}
