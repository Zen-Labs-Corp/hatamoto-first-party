package moderation

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Notice codes of this phase, namespaced "mod-" so they never collide with other phases.
const (
	nLogin       core.Notice = "mod-login"
	nMissing     core.Notice = "mod-missing"
	nFlagKarma   core.Notice = "mod-flag-karma"
	nFlagOwn     core.Notice = "mod-flag-own"
	nFlagDead    core.Notice = "mod-flag-dead"
	nFlagAgain   core.Notice = "mod-flag-again"
	nFlagged     core.Notice = "mod-flagged"
	nVouchKarma  core.Notice = "mod-vouch-karma"
	nVouchAlive  core.Notice = "mod-vouch-alive"
	nVouchLocked core.Notice = "mod-vouch-locked"
	nVouchOwn    core.Notice = "mod-vouch-own"
	nVouched     core.Notice = "mod-vouched"
	nBlocked     core.Notice = "mod-blocked"
	nDomain      core.Notice = "mod-domain"
	nNet         core.Notice = "mod-net"
	nVoteRate    core.Notice = "mod-vote-rate"
	nSignupRate  core.Notice = "mod-signup-rate"
	nLoginRate   core.Notice = "mod-login-rate"
	nNotPublic   core.Notice = "mod-not-public"
	nOff         core.Notice = "mod-off"
	nError       core.Notice = "mod-error"
)

var noticeTexts = map[core.Notice]string{
	nLogin:       "You have to be logged in to do that.",
	nMissing:     "That item does not exist.",
	nFlagKarma:   "You need more karma to flag.",
	nFlagOwn:     "You can't flag your own item.",
	nFlagDead:    "That item is already dead.",
	nFlagAgain:   "You have already flagged that.",
	nFlagged:     "Flagged.",
	nVouchKarma:  "You need more karma to vouch.",
	nVouchAlive:  "Only dead items can be vouched for.",
	nVouchLocked: "This item can't be vouched for.",
	nVouchOwn:    "You can't vouch for your own item.",
	nVouched:     "Vouched.",
	nBlocked:     "Your account can't do that.",
	nDomain:      "Submissions from that site are not allowed.",
	nNet:         "Your network can't do that right now.",
	nVoteRate:    "You are voting too fast. Please try again later.",
	nSignupRate:  "Too many sign-ups from your network. Please try again later.",
	nLoginRate:   "Too many login attempts. Please try again later.",
	nNotPublic:   "That comment is not public, so it was not killed.",
	nOff:         "That is not available yet.",
	nError:       "Something went wrong, please try again.",
}

// Rate limits (plan decision 6).
const (
	commentFlagsToDead = 3
	voteUserPerMin     = 30
	voteAddrPerMin     = 30
	voteNetPerMin      = 60
	signupNetPerHour   = 5
	signupAddrPerHour  = 3
	loginAddrPerHour   = 20
	loginNetPerHour    = 60
	loginUserPerHour   = 10
)

// defineNotices registers the texts of every notice code.
func defineNotices() {
	for code, text := range noticeTexts {
		core.DefineNotice(code, text)
	}
}

// refusal is a gate error: Notice() for HTML callers, Error() the same text for JSON callers.
type refusal struct{ n core.Notice }

func (e refusal) Error() string       { return core.NoticeText(string(e.n)) }
func (e refusal) Notice() core.Notice { return e.n }
