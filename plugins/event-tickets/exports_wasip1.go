//go:build wasip1

package main

// //go:wasmexport must sit in package main, one line per handled hook; the
// hooks are added with the code that handles them.

// main is never called: a c-shared wasm module is a reactor.
func main() {}
