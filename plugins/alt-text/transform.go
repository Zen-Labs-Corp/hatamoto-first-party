package main

import (
	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// transformStep is replaced by the real staging step.
func transformStep(sdk.RunStep) error {
	return guest.RunFinish(callHost, nil)
}
