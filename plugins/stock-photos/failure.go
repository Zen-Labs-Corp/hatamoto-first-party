package main

import (
	"errors"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// failRun ends the run with a message for the owner. It never returns an
// error: the host sees a clean step and a failed run.
func failRun(msg string) (sdk.RunStepResult, error) {
	_ = guest.RunFail(callHost, cleanText(msg, 500))
	return sdk.RunStepResult{}, nil
}

// failFromError ends the run for a provider or host error. Host error text is
// never passed on: it could echo a request.
func failFromError(err error) (sdk.RunStepResult, error) {
	var pe *providerError
	if errors.As(err, &pe) {
		return failRun(pe.Message)
	}
	var he *sdk.HostError
	if errors.As(err, &he) {
		switch he.Code {
		case sdk.ErrBlocked:
			return failRun("Tick api.pexels.com / api.openverse.org when you start the run.")
		case sdk.ErrCapabilityDenied:
			capability := he.Capability
			if capability == "" {
				capability = "http.fetch"
			}
			return failRun("Grant the " + capability + " permission to this plugin.")
		case sdk.ErrRateLimited:
			return failRun("Rate limited, try again in a few minutes.")
		}
	}
	return failRun("Stock photo search failed.")
}
