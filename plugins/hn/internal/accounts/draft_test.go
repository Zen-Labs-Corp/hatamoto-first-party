package accounts

import (
	"errors"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

func TestDraftFilter(t *testing.T) {
	draft := func(r interface {
		DraftComment(sdk.CommentDraft) (sdk.CommentDraft, error)
	}, key string) sdk.CommentDraft {
		t.Helper()
		out, err := r.DraftComment(sdk.CommentDraft{CommenterKey: key, AuthorName: "typed"})
		if err != nil {
			t.Fatal(err)
		}
		return out
	}

	t.Run("named key", func(t *testing.T) {
		f := installFake(t)
		f.add("k1", "bob")
		out := draft(newRegistry(nil), "k1")
		if out.AuthorName != "bob" || out.Reject != "" {
			t.Fatalf("got %+v", out)
		}
	})
	t.Run("unknown key", func(t *testing.T) {
		installFake(t)
		out := draft(newRegistry(nil), "nope")
		if out.AuthorName != "" || out.Reject != "Pick a username first." {
			t.Fatalf("got %+v", out)
		}
	})
	t.Run("empty key", func(t *testing.T) {
		f := installFake(t)
		out := draft(newRegistry(nil), "")
		if out.AuthorName != "" || out.Reject != "Pick a username first." || len(f.host.Calls) != 0 {
			t.Fatalf("got %+v, calls %v", out, f.host.Calls)
		}
	})
	t.Run("host error", func(t *testing.T) {
		f := installFake(t)
		f.queryErr = errors.New("down")
		out := draft(newRegistry(nil), "k1")
		if out.AuthorName != "" || out.Reject == "" || len(out.Reject) > sdk.CommentDraftMaxReject || strings.Contains(out.Reject, "typed") {
			t.Fatalf("got %+v", out)
		}
	})
}
