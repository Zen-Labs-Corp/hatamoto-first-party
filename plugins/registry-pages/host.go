package main

// callHost is the host API seam: guest.Call inside the wasm module (see
// host_wasip1.go), a fake in tests.
var callHost func(fn string, args, out any) error
