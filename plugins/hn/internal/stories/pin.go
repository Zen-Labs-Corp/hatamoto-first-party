package stories

import (
	"strconv"
	"strings"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const maxPins = 10

// pinSet turns a text argument such as "1,5" into a bigint array (a text arg, not an array bind).
const pinSet = "string_to_array($5, ',')::bigint[]"

// parsePinned keeps positive, unique item numbers in order, at most maxPins; anything else is skipped.
func parsePinned(s string) []int64 {
	var nos []int64
	seen := map[int64]bool{}
	for _, f := range strings.Split(s, ",") {
		n, err := strconv.ParseInt(strings.TrimSpace(f), 10, 64)
		if err != nil || n <= 0 || seen[n] {
			continue
		}
		seen[n] = true
		if nos = append(nos, n); len(nos) == maxPins {
			break
		}
	}
	return nos
}

// pinArg joins item numbers as "1,5,7".
func pinArg(nos []int64) string {
	parts := make([]string, len(nos))
	for i, n := range nos {
		parts[i] = strconv.FormatInt(n, 10)
	}
	return strings.Join(parts, ",")
}

// loadPinned returns the live, non-job, unhidden pinned stories in setting order.
func loadPinned(nos []int64, v *core.User) ([]storyRow, error) {
	var viewerID int64
	if v != nil {
		viewerID = v.ID
	}
	q := feedSelect("NOT s.dead AND s.kind <> 'job' AND s.item_no = ANY("+pinSet+")", "array_position("+pinSet+", s.item_no)", true)
	var rows []storyRow
	if err := core.QueryJSON(q, &rows, viewerID, false, len(nos), 0, pinArg(nos)); err != nil {
		return nil, err
	}
	return rows, nil
}
