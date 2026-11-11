package main

import (
	"errors"
	"mime"
	"net/http"
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	maxFormBody    = 8192
	maxDisplayName = 80
)

// text answers with a plain-text status page; used for refusals that must not
// redirect.
func text(code int, msg string) sdk.HTTPResponse {
	return sdk.HTTPResponse{
		Status:  code,
		Headers: map[string]string{"Content-Type": "text/plain; charset=utf-8", "Cache-Control": "no-store"},
		Body:    msg + "\n",
	}
}

// back sends the browser to the portal page, with a flash code on failure.
func back(code string, cookies ...sdk.SetCookie) sdk.HTTPResponse {
	if code != "" {
		cookies = append(cookies, flash(code))
	}
	return redirect(portalPath, cookies...)
}

// postForm is a POST that passed every check except what the handler decides.
type postForm struct {
	sess *session // nil when the browser has no valid session
	form url.Values
}

// guard runs the checks every POST route shares, cheapest first: the Origin
// must be the site, the body a small urlencoded form, then the session and its
// CSRF token. A non-nil response is the refusal.
func guard(r sdk.HTTPRequest, st settings) (postForm, *sdk.HTTPResponse) {
	refuse := func(code int, msg string) (postForm, *sdk.HTTPResponse) {
		resp := text(code, msg)
		return postForm{}, &resp
	}
	if st.PublicURL == "" || r.Headers["origin"] != st.PublicURL {
		return refuse(http.StatusForbidden, "forbidden")
	}
	if mt, _, err := mime.ParseMediaType(r.Headers["content-type"]); err != nil || mt != "application/x-www-form-urlencoded" {
		return refuse(http.StatusUnsupportedMediaType, "unsupported media type")
	}
	if r.BodyB64 != "" {
		return refuse(http.StatusBadRequest, "bad request")
	}
	if len(r.Body) > maxFormBody {
		return refuse(http.StatusRequestEntityTooLarge, "request too large")
	}
	form, err := url.ParseQuery(r.Body)
	if err != nil {
		return refuse(http.StatusBadRequest, "bad request")
	}
	sess, err := lookupSession(r.Cookies["sid"])
	if err != nil {
		return refuse(http.StatusServiceUnavailable, "try again later")
	}
	if sess == nil {
		return postForm{form: form}, nil
	}
	token := form.Get("csrf")
	if token == "" || !ctEq(token, sess.CSRF) {
		return refuse(http.StatusForbidden, "forbidden")
	}
	return postForm{sess: sess, form: form}, nil
}

// handleForm loads the settings, guards the request and runs act. A browser
// without a session just goes back to the page, which shows the sign-in.
func handleForm(r sdk.HTTPRequest, act func(postForm) sdk.HTTPResponse) sdk.HTTPResponse {
	st, err := loadSettings()
	if err != nil {
		return text(http.StatusServiceUnavailable, "try again later")
	}
	pf, refused := guard(r, st)
	if refused != nil {
		return *refused
	}
	if pf.sess == nil {
		return back("")
	}
	return act(pf)
}

// handleAccount creates the account of a signup session once the terms box is
// ticked.
func handleAccount(r sdk.HTTPRequest) sdk.HTTPResponse {
	return handleForm(r, func(pf postForm) sdk.HTTPResponse {
		s := pf.sess
		if s.AccountID != nil || s.GH.ID == 0 {
			return back("")
		}
		name := strings.TrimSpace(pf.form.Get("display_name"))
		if n := utf8.RuneCountInString(name); n < 1 || n > maxDisplayName || strings.IndexFunc(name, unicode.IsControl) >= 0 {
			return back("name")
		}
		email := strings.ToLower(strings.TrimSpace(pf.form.Get("email")))
		if email == "" || !contains(s.GH.Emails, email) {
			return back("email")
		}
		if pf.form.Get("terms") != "1" {
			return back("terms")
		}
		id, err := createAccount(s.GH, name, email)
		if err == nil {
			err = attachAccount(s.ID, id)
		}
		if err != nil {
			logf("warn", "account create failed")
			return back("failed")
		}
		return back("")
	})
}

// handleKey registers the account's one key.
func handleKey(r sdk.HTTPRequest) sdk.HTTPResponse {
	return handleForm(r, func(pf postForm) sdk.HTTPResponse {
		s := pf.sess
		if s.AccountID == nil {
			return back("")
		}
		if s.Status != "active" {
			return back("suspended")
		}
		b64, keyID, err := parseKey(pf.form.Get("key"))
		if err != nil {
			return back("key")
		}
		switch err := addKey(*s.AccountID, b64, keyID); {
		case err == nil:
			return back("")
		case errors.Is(err, errKeyTaken):
			return back("keytaken")
		case errors.Is(err, errHasKey):
			return back("haskey")
		case errors.Is(err, errInactive):
			return back("suspended")
		default:
			logf("warn", "key add failed")
			return back("failed")
		}
	})
}

// handleLogout ends the session. The cookie cannot be deleted through the
// host, so it is overwritten with an empty value.
func handleLogout(r sdk.HTTPRequest) sdk.HTTPResponse {
	return handleForm(r, func(pf postForm) sdk.HTTPResponse {
		if err := deleteSession(r.Cookies["sid"]); err != nil {
			logf("warn", "logout failed")
			return back("failed")
		}
		return back("", clearCookie("sid"))
	})
}

func contains(list []string, v string) bool {
	for _, x := range list {
		if x == v {
			return true
		}
	}
	return false
}
