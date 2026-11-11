package main

import (
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	termsPath  = "/p/devportal/terms"
	dateLayout = "2 Jan 2006"
)

// flashMessages are the texts behind the one-shot flash codes.
var flashMessages = map[string]string{
	"state":      "That sign-in link expired or did not start here. Please try again.",
	"denied":     "GitHub sign-in was cancelled.",
	"noemail":    "Your GitHub account has no verified email address. Verify one on GitHub and try again.",
	"suspended":  "This developer account is suspended.",
	"failed":     "Something went wrong. Please try again in a moment.",
	"name":       "Enter a display name of up to 80 characters.",
	"email":      "Pick one of your verified GitHub emails.",
	"terms":      "Accept the Developer terms to create your account.",
	"key":        "That is not a valid public key. Paste the base64 public key, not the private one.",
	"privatekey": "That is a private key. Do not share it: treat it as exposed, generate a new key pair and register the public key (the .pub file).",
	"keytaken":   "That key is already registered to another account.",
	"haskey":     "This account already has a key.",
}

// pageFilter fills the portal section of the /developers page. Every other
// path returns at once, before any settings read or host call: the hook runs
// on every public page view of the site.
func pageFilter(p sdk.PageContext) (sdk.PageContext, error) {
	if p.Path != portalPath {
		return p, nil
	}
	if len(p.Blocks) == 0 {
		return p, nil
	}
	idx := 0
	for i, b := range p.Blocks {
		if b.Fields["anchor"] == "devportal" {
			idx = i
			break
		}
	}
	st, err := loadSettings()
	if err != nil {
		return p, nil // fail open: the placeholder block stays
	}
	sess, err := lookupSession(p.Cookies["sid"])
	if err != nil {
		return p, nil
	}

	c := map[string]any{"terms_url": termsPath, "flash": ""}
	var cookies []sdk.SetCookie
	if code := p.Cookies["fl"]; code != "" {
		c["flash"] = flashMessages[code]
		cookies = append(cookies, clearCookie("fl"))
	}
	if p.Cookies["sid"] != "" && sess == nil {
		cookies = append(cookies, clearCookie("sid"))
	}

	if sess != nil && sess.Slid {
		// The browser drops the cookie after its own Max-Age: renew it with the row.
		cookies = append(cookies, sdk.SetCookie{Name: "sid", Value: p.Cookies["sid"], MaxAge: int(sessionTTL / time.Second)})
	}

	switch {
	case sess == nil:
		if st.ClientID == "" || st.PublicURL == "" {
			c["state"] = "notice"
			c["flash"] = "Developer sign-in is not available yet."
			break
		}
		state := p.Cookies["st"]
		if state == "" {
			state = newToken()
			cookies = append(cookies, sdk.SetCookie{Name: "st", Value: state, MaxAge: stateTTL})
		}
		c["state"] = "signed_out"
		c["authorize_url"] = authorizeURL(st, state)
	case sess.AccountID == nil:
		c["state"] = "signup"
		c["csrf"] = sess.CSRF
		c["login"] = sess.GH.Login
		setEmails(c, sess.GH.Emails)
	case sess.Status != "active":
		c["state"] = "suspended"
		c["login"] = sess.Login
	default:
		c["state"] = "dashboard"
		c["csrf"] = sess.CSRF
		c["login"] = sess.Login
		c["display_name"] = sess.DisplayName
		c["plan"] = planLabel(sess.Plan)
		c["has_key"] = sess.KeyID != ""
		c["key_id"] = sess.KeyID
		if sess.KeyID != "" && !sess.KeyAdded.IsZero() {
			c["key_added"] = sess.KeyAdded.UTC().Format(dateLayout)
		}
	}

	b := p.Blocks[idx]
	p.Blocks[idx] = sdk.Block{ID: b.ID, Type: "devportal", Title: b.Title, Content: c}
	p.SetCookies = append(p.SetCookies, cookies...)
	return p, nil
}

// setEmails puts the emails in as a list and, as a fallback for a host that
// drops nested content, as the flat keys email1..email5.
func setEmails(c map[string]any, emails []string) {
	list := make([]any, 0, len(emails))
	for i, e := range emails {
		list = append(list, e)
		if i < 5 {
			c["email"+strconv.Itoa(i+1)] = e
		}
	}
	c["emails"] = list
}

func planLabel(plan string) string {
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}
