package comments

import (
	"errors"
	"regexp"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

func hostCode(err error, code string) bool {
	var he *sdk.HostError
	return errors.As(err, &he) && he.Code == code
}

// isUnavailable reports a host function that is not shipped, or whose backing service is off.
func isUnavailable(err error) bool { return hostCode(err, sdk.ErrUnavailable) }

// isRateLimited reports a host function that refused the call for rate.
func isRateLimited(err error) bool { return hostCode(err, sdk.ErrRateLimited) }

// hostless reports a run without a host (unit runs call crons with core.Call == nil).
func hostless() bool { return core.Call == nil }

var uuidRe = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// validID reports whether s is a uuid; ids reach SQL only after this check.
func validID(s string) bool { return uuidRe.MatchString(strings.ToLower(s)) }
