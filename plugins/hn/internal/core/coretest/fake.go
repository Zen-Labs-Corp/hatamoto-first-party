// Package coretest is a fake host for the tests of every phase package. Non-test code never imports it.
package coretest

import (
	"encoding/json"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Host answers host functions by name.
type Host struct {
	// Calls lists every function called, in order.
	Calls []string
	// Fns maps a host function name to its answer; args is the marshalled argument.
	Fns map[string]func(args json.RawMessage) (any, error)
}

// Install makes h the host for the test and restores "no host" at cleanup.
func Install(t testing.TB, h *Host) {
	t.Helper()
	core.Call = func(fn string, args, out any) error {
		h.Calls = append(h.Calls, fn)
		f, ok := h.Fns[fn]
		if !ok {
			t.Errorf("coretest: unexpected host call %q", fn)
			return errUnknown(fn)
		}
		raw, err := json.Marshal(args)
		if err != nil {
			return err
		}
		res, err := f(raw)
		if err != nil {
			return err
		}
		if out == nil || res == nil {
			return nil
		}
		b, err := json.Marshal(res)
		if err != nil {
			return err
		}
		return json.Unmarshal(b, out)
	}
	t.Cleanup(func() { core.Call = nil })
}

type errUnknown string

func (e errUnknown) Error() string { return "coretest: no fake for host function " + string(e) }

// Rows builds a db.query answer with one row of the given cells.
func Rows(cells ...any) map[string]any {
	return map[string]any{"rows": [][]any{cells}}
}
