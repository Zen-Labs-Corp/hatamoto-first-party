//go:build wasip1

package main

import (
	"hatamoto.dev/sdk/guest"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func init() { core.Call = guest.Call }
