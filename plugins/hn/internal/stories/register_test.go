package stories

import (
	"errors"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func TestRegisterTwiceIsSafe(t *testing.T) {
	newRegistry(t, nil)
	newRegistry(t, nil)
	for code := range noticeTexts {
		if core.NoticeText(string(code)) == "" {
			t.Errorf("notice %q has no text", code)
		}
	}
}

type codedErr struct{ code core.Notice }

func (e codedErr) Error() string       { return "refused" }
func (e codedErr) Notice() core.Notice { return e.code }

func TestNoticeOf(t *testing.T) {
	newRegistry(t, nil)
	if got := noticeOf(errors.New("plain")); got != nRefused {
		t.Errorf("plain error = %q", got)
	}
	if got := noticeOf(codedErr{nRate}); got != nRate {
		t.Errorf("defined code = %q", got)
	}
	if got := noticeOf(codedErr{"nope-undefined"}); got != nRefused {
		t.Errorf("undefined code = %q", got)
	}
}
