//go:build wasip1

package main

import "hatamoto.dev/sdk/guest"

func init() { callHost = guest.Call }
