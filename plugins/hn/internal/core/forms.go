package core

// Paths of the account routes and core's email-link sign-in. The login forms
// (partial plugin/hn/login_forms) are shown on /login and on /submit.
const (
	LoginAction    = "/p/hn/login"
	SignupAction   = "/p/hn/signup"
	EmailSignInURL = "/-/comments/signin"
)

// LoginForms is the content the plugin/hn/login_forms partial reads. next is
// where a successful login returns to (a safe path; "/" when not).
func LoginForms(next string) map[string]any {
	if !SafePath(next) {
		next = "/"
	}
	return map[string]any{"login_action": LoginAction, "signup_action": SignupAction, "email_signin_href": EmailSignInURL, "next": next}
}
