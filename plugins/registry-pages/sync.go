package main

import "hatamoto.dev/sdk"

// syncCron is replaced by the registry-entry sync implementation.
func syncCron(sdk.CronEvent) error { return nil }
