package main

import "time"

// callHost is the host API seam: guest.Call inside the wasm module (see
// host_wasip1.go), a fake in tests.
var callHost func(fn string, args, out any) error

// now is the clock of the step budgets; a fake in tests.
var now = time.Now
