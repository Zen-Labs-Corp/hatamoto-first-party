package stories

import (
	"errors"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Notice codes of this phase, namespaced "story-" so they never collide with other phases.
const (
	nLogin       core.Notice = "story-login"
	nTitle       core.Notice = "story-title"
	nURL         core.Notice = "story-url"
	nEmpty       core.Notice = "story-empty"
	nTextLong    core.Notice = "story-text-long"
	nRate        core.Notice = "story-rate"
	nDup         core.Notice = "story-dup"
	nOff         core.Notice = "story-off"
	nBusy        core.Notice = "story-busy"
	nError       core.Notice = "story-error"
	nRefused     core.Notice = "story-refused"
	nMissing     core.Notice = "story-missing"
	nVoteOwn     core.Notice = "story-vote-own"
	nVoteJob     core.Notice = "story-vote-job"
	nVoteDead    core.Notice = "story-vote-dead"
	nUnvoteLate  core.Notice = "story-unvote-late"
	nNotAuthor   core.Notice = "story-not-author"
	nWindowOver  core.Notice = "story-window"
	nHasComments core.Notice = "story-has-comments"
	nSaved       core.Notice = "story-saved"
	nDeleted     core.Notice = "story-deleted"
)

var noticeTexts = map[core.Notice]string{
	nLogin:       "You have to be logged in to do that.",
	nTitle:       "Please give the story a title of 1 to 80 characters.",
	nURL:         "That url is not a valid http or https link.",
	nEmpty:       "Please submit a url or some text.",
	nTextLong:    "The text is too long (10000 characters at most).",
	nRate:        "You are submitting too fast. Please try again later.",
	nDup:         "That link was posted recently, here is the discussion.",
	nOff:         "Submitting is not available yet.",
	nBusy:        "Too many submissions right now. Please try again in a minute.",
	nError:       "Something went wrong, please try again.",
	nRefused:     "That was refused.",
	nMissing:     "That story does not exist.",
	nVoteOwn:     "You can't vote on your own story.",
	nVoteJob:     "Job posts can't be voted on.",
	nVoteDead:    "Dead stories can't be voted on.",
	nUnvoteLate:  "Votes can only be undone within an hour.",
	nNotAuthor:   "Only the author can change this story.",
	nWindowOver:  "This story can no longer be changed.",
	nHasComments: "A story with comments can't be deleted.",
	nSaved:       "Saved.",
	nDeleted:     "Story deleted.",
}

// defineNotices registers the texts of every notice code.
func defineNotices() {
	for code, text := range noticeTexts {
		core.DefineNotice(code, text)
	}
}

// noticer is implemented by a gate error that carries a notice code of its own.
type noticer interface{ Notice() core.Notice }

// noticeOf is the notice code of a refusal: the one the error carries when it
// is a defined code, else the generic nRefused.
func noticeOf(err error) core.Notice {
	var n noticer
	if errors.As(err, &n) {
		if code := n.Notice(); core.NoticeText(string(code)) != "" {
			return code
		}
	}
	return nRefused
}
