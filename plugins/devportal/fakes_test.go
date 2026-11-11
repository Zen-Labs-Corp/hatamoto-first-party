package main

import (
	"encoding/json"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

// hostX wraps the installed callHost (normally a fakeHost) and answers the
// host functions the submission flow adds: packages.inspect, mail.send and
// log. Everything else goes to the wrapped fake.
type hostX struct {
	inspect    sdk.InspectResult
	inspectErr error
	inspected  []sdk.InspectRequest
	mailErr    error
	mailTo     []string
	logs       []string
}

func (x *hostX) install(t *testing.T) {
	t.Helper()
	inner := callHost
	t.Cleanup(func() { callHost = inner })
	callHost = func(fn string, args, out any) error {
		switch fn {
		case "packages.inspect":
			x.inspected = append(x.inspected, args.(sdk.InspectRequest))
			if x.inspectErr != nil {
				return x.inspectErr
			}
			res := x.inspect
			if res.Problems == nil {
				res.Problems = []string{}
			}
			b, _ := json.Marshal(res)
			return json.Unmarshal(b, out)
		case "mail.send":
			m := args.(map[string]any)
			if x.mailErr != nil {
				return x.mailErr
			}
			x.mailTo = append(x.mailTo, m["to"].([]string)...)
			return nil
		case "log":
			x.logs = append(x.logs, args.(map[string]string)["message"])
			return nil
		}
		return inner(fn, args, out)
	}
}

func hostErr(code string) error { return &sdk.HostError{Code: code, Message: "x"} }

var errPlain = errors.New("plain failure")
