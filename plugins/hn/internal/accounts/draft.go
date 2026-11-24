package accounts

import (
	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

const (
	rejectNoName      = "Pick a username first."
	rejectUnavailable = "Sign-in name unavailable, try again."
)

// draftName signs a comment with the author's username. It fails closed: an
// unknown key or a host error rejects the draft, never lets the typed name through.
func draftName(d sdk.CommentDraft) (core.DraftAnswer, error) {
	if d.CommenterKey == "" {
		return core.DraftAnswer{Reject: rejectNoName}, nil
	}
	u, err := core.UserByKey(d.CommenterKey)
	if err != nil {
		return core.DraftAnswer{Reject: rejectUnavailable}, nil
	}
	if u == nil {
		return core.DraftAnswer{Reject: rejectNoName}, nil
	}
	return core.DraftAnswer{Name: u.Username}, nil
}
