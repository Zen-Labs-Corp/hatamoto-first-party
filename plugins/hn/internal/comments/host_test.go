package comments

import (
	"errors"
	"fmt"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

func TestHostHelpers(t *testing.T) {
	unavailable := &sdk.HostError{Code: sdk.ErrUnavailable}
	limited := &sdk.HostError{Code: sdk.ErrRateLimited}

	if !isUnavailable(unavailable) || !isUnavailable(fmt.Errorf("set rank: %w", unavailable)) {
		t.Error("unavailable not recognised")
	}
	if isUnavailable(limited) || isUnavailable(errors.New("boom")) || isUnavailable(nil) {
		t.Error("isUnavailable accepts another error")
	}
	if !isRateLimited(limited) || !isRateLimited(fmt.Errorf("x: %w", limited)) {
		t.Error("rate_limited not recognised")
	}
	if isRateLimited(unavailable) || isRateLimited(errors.New("boom")) || isRateLimited(nil) {
		t.Error("isRateLimited accepts another error")
	}

	for id, want := range map[string]bool{
		cid1:                                   true,
		"AAAAAAAA-AAAA-4AAA-8AAA-AAAAAAAAAAAA": true,
		"":                                     false,
		"not-a-uuid":                           false,
		cid1 + "0":                             false,
		cid1[:35]:                              false,
		"0000000g-0000-4000-8000-000000000001": false,
		"'; drop table comment_meta; --":       false,
	} {
		if got := validID(id); got != want {
			t.Errorf("validID(%q) = %v, want %v", id, got, want)
		}
	}

	prev := core.Call
	core.Call = nil
	if !hostless() {
		t.Error("hostless false without a host")
	}
	core.Call = func(string, any, any) error { return nil }
	if hostless() {
		t.Error("hostless true with a host")
	}
	core.Call = prev

	// Register twice in one process: notices are idempotent.
	newRegistry(nil)
	newRegistry(nil)
}
