package main

import (
	"errors"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// errEndRun is returned when the run could not be ended; the SDK then fails
// the step. It is a fixed text: a host error could echo a request.
var errEndRun = errors.New("could not end the run")

// failRun ends the run with a message for the owner. If the host refuses the
// failure itself, the step returns an error instead of ending with nothing.
func failRun(msg string) (sdk.RunStepResult, error) {
	if err := guest.RunFail(callHost, cleanText(msg, 500)); err != nil {
		return sdk.RunStepResult{}, errEndRun
	}
	return sdk.RunStepResult{}, nil
}

// failFromError ends the run for a provider or host error.
func failFromError(err error) (sdk.RunStepResult, error) {
	return failRun(errorMessage(err))
}

// errorMessage is the owner-facing text for a provider or host error. Host
// error text is never passed on: it could echo a request.
func errorMessage(err error) string {
	var pe *providerError
	if errors.As(err, &pe) {
		return pe.Message
	}
	var he *sdk.HostError
	if errors.As(err, &he) {
		switch he.Code {
		case sdk.ErrBlocked:
			return "Tick api.pexels.com / api.openverse.org when you start the run."
		case sdk.ErrCapabilityDenied:
			capability := he.Capability
			if capability == "" {
				capability = "http.fetch"
			}
			return "Grant the " + capability + " permission to this plugin."
		case sdk.ErrRateLimited:
			return "Rate limited, try again in a few minutes."
		}
	}
	return "Stock photo search failed."
}
