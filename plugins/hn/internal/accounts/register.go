package accounts

import "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"

// Paths: the core email-link sign-in (for members who signed up that way) and this plugin's
// own session routes.
const (
	signInURL      = core.EmailSignInURL
	loginAction    = core.LoginAction
	signupAction   = core.SignupAction
	logoutAction   = "/p/hn/logout"
	logoutAll      = "/p/hn/logout/all"
	passwordAction = "/p/hn/password"         // change, password members only
	connectAction  = "/p/hn/password/connect" // explicit connect of an email-link member (the only link route)
)

// Notice codes of this phase (static texts, defined in Register).
const (
	noticeSigninFirst   core.Notice = "signin_first"
	noticeNameBad       core.Notice = "name_bad"
	noticeNameReserved  core.Notice = "name_reserved"
	noticeNameTaken     core.Notice = "name_taken"
	noticeNameHas       core.Notice = "name_has"
	noticeSignupRefused core.Notice = "signup_refused"
	noticeTryAgain      core.Notice = "try_again"
	noticeWelcome       core.Notice = "welcome"
	noticeProfileSaved  core.Notice = "profile_saved"
	noticePwBad         core.Notice = "pw_bad"
)

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	core.DefineNotice(noticeSigninFirst, "Sign in first.")
	core.DefineNotice(noticeNameBad, "Usernames are 2 to 15 letters, digits, - or _.")
	core.DefineNotice(noticeNameReserved, "That username is reserved.")
	core.DefineNotice(noticeNameTaken, "That username is taken.")
	core.DefineNotice(noticeNameHas, "You already have a username.")
	core.DefineNotice(noticeSignupRefused, "Sign-up is not possible right now.")
	core.DefineNotice(noticeTryAgain, "Something went wrong, try again.")
	core.DefineNotice(noticeWelcome, "Welcome! Your account is ready.")
	core.DefineNotice(noticeProfileSaved, "Profile updated.")
	core.DefineNotice(noticePwBad, "Passwords are 8 to 72 characters.")

	r.Route("POST /username", claimHandler(r))
	r.Route("POST /profile", profileHandler)
	r.Route("POST /signup", sessionRoute(signupHandler(r)))
	r.Draft(draftName)
	r.PageAll(userbar)
	r.Page("/login", loginPage)
	r.Page("/user", userPage)
}
