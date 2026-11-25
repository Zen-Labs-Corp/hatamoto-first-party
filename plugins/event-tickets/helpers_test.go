package main

import (
	"encoding/json"
	"regexp"
	"strconv"
)

var regexpHex32 = regexp.MustCompile(`^[0-9a-f]{32}$`)

func itoa(n int64) string { return strconv.FormatInt(n, 10) }

// jsonInto decodes a canned host answer into out.
func jsonInto(raw string, out any) error { return json.Unmarshal([]byte(raw), out) }
