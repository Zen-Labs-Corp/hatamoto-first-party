package comments

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Notice codes of this phase. Codes are global: signin_first is also defined
// by accounts, with the same text.
const (
	nSignin  core.Notice = "signin_first"
	nName    core.Notice = "comment-name"
	nMissing core.Notice = "comment-missing"
	nDead    core.Notice = "comment-dead"
	nOwn     core.Notice = "comment-own"
	nKarma   core.Notice = "comment-karma"
	nVoted   core.Notice = "comment-voted"
	nNoVote  core.Notice = "comment-novote"
	nLate    core.Notice = "comment-late"
	nBad     core.Notice = "comment-bad"
	nRefused core.Notice = "comment-refused"
	nError   core.Notice = "comment-error"
)

func defineNotices() {
	core.DefineNotice(nSignin, "Sign in first.")
	core.DefineNotice(nName, "Pick a username first.")
	core.DefineNotice(nMissing, "That comment cannot be voted on.")
	core.DefineNotice(nDead, "You can't vote on that comment.")
	core.DefineNotice(nOwn, "You can't vote on your own comment.")
	core.DefineNotice(nKarma, "You need more karma to downvote.")
	core.DefineNotice(nVoted, "You already voted on that comment.")
	core.DefineNotice(nNoVote, "There is no vote to undo.")
	core.DefineNotice(nLate, "Votes can only be undone within an hour.")
	core.DefineNotice(nBad, "Unknown vote.")
	core.DefineNotice(nRefused, "That vote was refused.")
	core.DefineNotice(nError, "Something went wrong, please try again.")
}
