package core

import "time"

// Now is the clock; tests replace it.
var Now = func() time.Time { return time.Now().UTC() }
