package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	portalPath      = "/developers"
	callbackPath    = "/p/devportal/auth/callback"
	githubScopes    = "read:user user:email"
	stateTTL        = 600 // seconds: issued-state marker and the st cookie
	flashTTL        = 60
	fetchTimeoutMS  = 4000
	maxStateLen     = 64
	maxGitHubEmails = 10
)

// issuedKey is the kv key that proves the page filter minted a state.
func issuedKey(state string) string { return "issued:" + state }

// authorizeURL is where the "Sign in with GitHub" link points.
func authorizeURL(s settings, state string) string {
	q := url.Values{
		"client_id":    {s.ClientID},
		"redirect_uri": {s.PublicURL + callbackPath},
		"scope":        {githubScopes},
		"state":        {state},
		"allow_signup": {"true"},
	}
	return cfg("github_authorize_url") + "?" + q.Encode()
}

func logf(level, format string, a ...any) {
	_ = callHost("log", map[string]string{"level": level, "message": fmt.Sprintf(format, a...)}, nil)
}

// flash is the cookie that carries a one-shot message code to the page.
func flash(code string) sdk.SetCookie {
	return sdk.SetCookie{Name: "fl", Value: code, MaxAge: flashTTL}
}

// clearCookie overwrites a cookie: the host cannot delete one, and rejects a
// negative max_age.
func clearCookie(name string) sdk.SetCookie { return sdk.SetCookie{Name: name, Value: "", MaxAge: 1} }

// callbackFail sends the browser back to the page with a message and without
// the state cookie. stage and status go to the log, nothing else.
func callbackFail(code, stage string, status int) sdk.HTTPResponse {
	if stage != "" {
		logf("warn", "callback failed: %s status=%d", stage, status)
	}
	return redirect(portalPath, clearCookie("st"), flash(code))
}

// handleCallback finishes the GitHub sign-in. It never returns a Go error:
// every failure is a redirect with a flash code.
func handleCallback(r sdk.HTTPRequest) sdk.HTTPResponse {
	st, err := loadSettings()
	if err != nil || st.ClientID == "" || st.ClientSecret == "" || st.PublicURL == "" {
		return callbackFail("failed", "settings", 0)
	}
	state, cookie := first(r.Query["state"]), r.Cookies["st"]
	if state == "" || cookie == "" || len(state) > maxStateLen || !ctEq(state, cookie) {
		return callbackFail("state", "", 0)
	}
	// The state must be one this plugin issued, and it is spent here: a cookie
	// and query pair a client made up has no marker. The host has no atomic
	// if-absent write, so two simultaneous callbacks can both pass; GitHub's
	// code is single use and stops the second.
	var issued struct {
		Found bool `json:"found"`
	}
	if err := callHost("kv.get", map[string]string{"key": issuedKey(state)}, &issued); err != nil || !issued.Found {
		return callbackFail("state", "", 0)
	}
	if err := callHost("kv.delete", map[string]string{"key": issuedKey(state)}, nil); err != nil {
		return callbackFail("failed", "state-store", 0)
	}
	code := first(r.Query["code"])
	if first(r.Query["error"]) != "" || code == "" {
		return callbackFail("denied", "", 0)
	}

	token, stage, status := exchangeCode(st, code)
	if token == "" {
		return callbackFail("failed", stage, status)
	}
	gh, stage, status := readIdentity(token)
	if stage != "" {
		return callbackFail("failed", stage, status)
	}

	acct, err := signInAccount(gh.ID, gh.Login)
	if err != nil {
		return callbackFail("failed", "account-lookup", 0)
	}
	var tok string
	switch {
	case acct != nil && acct.Status != "active":
		return callbackFail("suspended", "", 0)
	case acct != nil:
		tok, _, err = createSession(&acct.ID, ghIdentity{})
	case len(gh.Emails) == 0:
		return callbackFail("noemail", "", 0)
	default:
		tok, _, err = createSession(nil, gh)
	}
	if err != nil {
		return callbackFail("failed", "session", 0)
	}
	return redirect(portalPath, clearCookie("st"), sdk.SetCookie{Name: "sid", Value: tok, MaxAge: int(sessionTTL / time.Second)})
}

// fetched is the answer of http.fetch.
type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

// ghFetch calls GitHub through the host. A truncated or non-200 answer is an
// error: half a JSON document only hides the cause.
func ghFetch(method, rawURL string, headers map[string]string, body string) (fetched, error) {
	var res fetched
	err := callHost("http.fetch", map[string]any{
		"method": method, "url": rawURL, "headers": headers, "body": body, "timeout_ms": fetchTimeoutMS,
	}, &res)
	switch {
	case err != nil:
		return res, err
	case res.Truncated:
		return res, errors.New("answer too large")
	case res.Status != http.StatusOK:
		return res, errors.New("unexpected status")
	}
	return res, nil
}

// exchangeCode trades the code for an access token. The token lives only for
// the two reads that follow. On failure it returns the stage and HTTP status
// for the log.
func exchangeCode(st settings, code string) (token, stage string, status int) {
	form := url.Values{
		"client_id":     {st.ClientID},
		"client_secret": {st.ClientSecret},
		"code":          {code},
		"redirect_uri":  {st.PublicURL + callbackPath},
	}
	res, err := ghFetch("POST", cfg("github_token_url"), map[string]string{
		"Accept": "application/json", "Content-Type": "application/x-www-form-urlencoded",
	}, form.Encode())
	if err != nil {
		return "", "token", res.Status
	}
	var out struct {
		AccessToken string `json:"access_token"`
		Error       string `json:"error"`
	}
	if json.Unmarshal([]byte(res.Body), &out) != nil || out.Error != "" || out.AccessToken == "" {
		return "", "token-body", res.Status
	}
	return out.AccessToken, "", 0
}

// ghEmail is one entry of GitHub's /user/emails.
type ghEmail struct {
	Email    string `json:"email"`
	Verified bool   `json:"verified"`
	Primary  bool   `json:"primary"`
}

// readIdentity reads the profile and the verified emails (primary first).
func readIdentity(token string) (gh ghIdentity, stage string, status int) {
	h := map[string]string{
		"Authorization": "Bearer " + token,
		"User-Agent":    "hatamoto-devportal",
		"Accept":        "application/vnd.github+json",
	}
	res, err := ghFetch("GET", cfg("github_api_url")+"/user", h, "")
	if err != nil {
		return gh, "user", res.Status
	}
	var user struct {
		ID    int64  `json:"id"`
		Login string `json:"login"`
	}
	if json.Unmarshal([]byte(res.Body), &user) != nil || user.ID <= 0 || user.Login == "" {
		return gh, "user-body", res.Status
	}
	gh.ID, gh.Login = user.ID, user.Login

	res, err = ghFetch("GET", cfg("github_api_url")+"/user/emails", h, "")
	if err != nil {
		return gh, "emails", res.Status
	}
	var emails []ghEmail
	if json.Unmarshal([]byte(res.Body), &emails) != nil {
		return gh, "emails-body", res.Status
	}
	slices.SortStableFunc(emails, func(a, b ghEmail) int {
		switch {
		case a.Primary && !b.Primary:
			return -1
		case !a.Primary && b.Primary:
			return 1
		}
		return 0
	})
	for _, e := range emails {
		addr := strings.ToLower(strings.TrimSpace(e.Email))
		if !e.Verified || addr == "" || slices.Contains(gh.Emails, addr) {
			continue
		}
		if len(gh.Emails) < maxGitHubEmails {
			gh.Emails = append(gh.Emails, addr)
		}
	}
	return gh, "", 0
}
