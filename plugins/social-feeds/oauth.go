package main

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	stateTTL         = 600 // seconds a started flow stays valid
	authorizeURL     = "https://www.instagram.com/oauth/authorize"
	shortTokenURL    = "https://api.instagram.com/oauth/access_token"
	graphURL         = "https://graph.instagram.com/"
	minTokenAge      = 24 * time.Hour
	renewWindow      = 7 * 24 * time.Hour
	defaultLongLived = 60 * 24 * 3600  // seconds, when Instagram omits expires_in
	callbackBudget   = 9 * time.Second // inside the 10 s route timeout
	lookupMinLeft    = 1500 * time.Millisecond
)

// randomToken is n random bytes in hex.
func randomToken(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic(err) // the host's random source failing is not recoverable
	}
	return hex.EncodeToString(b)
}

func (s settings) missingForConnect() []string {
	var m []string
	if s.AppID == "" {
		m = append(m, "instagram_app_id")
	}
	if s.AppSecret == "" {
		m = append(m, "instagram_app_secret")
	}
	if s.PublicURL == "" {
		m = append(m, "public_url")
	}
	return m
}

// connectInstagram begins the flow. Navigated to (Accept: text/html) it sends
// the browser on to Instagram; asked for JSON it returns the address.
func connectInstagram(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	if m := s.missingForConnect(); len(m) > 0 {
		return reply(http.StatusConflict, map[string]any{"error": "fill in the plugin settings first", "missing": m})
	}
	state := randomToken(16)
	uid := ""
	if r.User != nil {
		uid = r.User.ID
	}
	err := callHost("kv.set", map[string]any{"key": "oauth:" + state, "value": map[string]string{"user_id": uid}, "ttl_seconds": stateTTL}, nil)
	if err != nil {
		return fail(err)
	}
	q := url.Values{}
	q.Set("client_id", s.AppID)
	q.Set("redirect_uri", s.redirectURI())
	q.Set("response_type", "code")
	q.Set("scope", "instagram_business_basic")
	q.Set("state", state)
	target := authorizeURL + "?" + q.Encode()
	if strings.Contains(r.Headers["accept"], "text/html") {
		link := html.EscapeString(target)
		return page(http.StatusOK, "Connecting to Instagram",
			`<meta http-equiv="refresh" content="0;url=`+link+`"><p><a href="`+link+`">Continue to Instagram</a></p>`, nil), nil
	}
	return reply(http.StatusOK, map[string]string{"authorization_url": target})
}

// oauthCallback is where Instagram sends the owner back. It is public, so the
// one-time state (random, 10 minutes) is what proves this browser began the
// flow. Nothing else happens, and no call leaves the plugin, before it checks.
func oauthCallback(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	state := first(r.Query["state"])
	if !validState(state) {
		return expired()
	}
	var got struct {
		Found bool `json:"found"`
	}
	if err := callHost("kv.get", map[string]string{"key": "oauth:" + state}, &got); err != nil {
		return fail(err)
	}
	// The state is single use: whatever happens next, it is gone.
	_ = callHost("kv.delete", map[string]string{"key": "oauth:" + state}, nil)
	if !got.Found {
		return expired()
	}
	if e := first(r.Query["error"]); e != "" {
		return page(http.StatusBadRequest, "Instagram did not connect",
			"<p>Instagram answered: "+html.EscapeString(shorten(e, 100))+".</p>"+backText, nil), nil
	}
	code := strings.TrimSuffix(first(r.Query["code"]), "#_")
	if code == "" || len(code) > 1024 || len(s.missingForConnect()) > 0 {
		return page(http.StatusBadRequest, "Link not valid", "<p>This address is not an Instagram answer.</p>"+backText, nil), nil
	}
	budgetEnd = now().Add(callbackBudget)
	defer func() { budgetEnd = time.Time{} }()
	tok, err := exchangeCode(s, code)
	if err != nil {
		msg := redact(err.Error(), s.AppSecret)
		logf("warn", "code exchange: %s", msg)
		return page(http.StatusBadGateway, "Instagram refused the code", "<p>"+html.EscapeString(msg)+"</p>"+backText, nil), nil
	}
	// Save before the optional name lookup, so a slow lookup cannot lose the token.
	if err := saveToken(tok); err != nil {
		return fail(err)
	}
	if budgetEnd.Sub(now()) >= lookupMinLeft {
		if id, name := lookupAccount(tok.AccessToken); name != "" {
			tok.Username = name
			if id != "" {
				tok.UserID = id
			}
			if err := saveToken(tok); err != nil {
				return fail(err)
			}
		}
	}
	logf("info", "connected to Instagram")
	// The admin address is private and unknown to the plugin, so no redirect.
	return page(http.StatusOK, "Instagram connected", "<p>Connected. You can close this tab and go back to the plugin page.</p>", nil), nil
}

// validState accepts only what randomToken(16) makes: 32 lowercase hex digits.
func validState(s string) bool {
	if len(s) != 32 {
		return false
	}
	for _, c := range s {
		if !(c >= '0' && c <= '9' || c >= 'a' && c <= 'f') {
			return false
		}
	}
	return true
}

func expired() (sdk.HTTPResponse, error) {
	return page(http.StatusBadRequest, "Link expired", "<p>Start the connection again from the plugin page.</p>"+backText, nil), nil
}

const backText = "<p>Go back to the plugin page to try again.</p>"

// longLived is the answer of the exchange and refresh endpoints.
type longLived struct {
	AccessToken string `json:"access_token"`
	ExpiresIn   int64  `json:"expires_in"`
}

// exchangeCode trades the code for a short-lived token, that for a long-lived
// one.
func exchangeCode(s settings, code string) (igToken, error) {
	form := url.Values{}
	form.Set("client_id", s.AppID)
	form.Set("client_secret", s.AppSecret)
	form.Set("grant_type", "authorization_code")
	form.Set("redirect_uri", s.redirectURI())
	form.Set("code", code)
	res, err := fetch("POST", shortTokenURL, map[string]string{"Content-Type": "application/x-www-form-urlencoded"}, form.Encode())
	if err != nil {
		return igToken{}, err
	}
	if !ok2xx(res.Status) {
		return igToken{}, apiError(res.Status, res.Body)
	}
	short, userID, err := parseShortToken(res.Body)
	if err != nil {
		return igToken{}, err
	}

	q := url.Values{}
	q.Set("grant_type", "ig_exchange_token")
	q.Set("client_secret", s.AppSecret)
	q.Set("access_token", short)
	long, err := getLongLived(graphURL + "access_token?" + q.Encode())
	if err != nil {
		return igToken{}, err
	}
	return newToken(long, userID, ""), nil
}

// parseShortToken reads both shapes of the short-lived answer: flat, or
// wrapped in {"data":[...]}.
func parseShortToken(body string) (token, userID string, err error) {
	var doc struct {
		AccessToken string      `json:"access_token"`
		UserID      json.Number `json:"user_id"`
		Data        []struct {
			AccessToken string      `json:"access_token"`
			UserID      json.Number `json:"user_id"`
		} `json:"data"`
	}
	if json.Unmarshal([]byte(body), &doc) != nil {
		return "", "", errors.New("Instagram answered with something unreadable")
	}
	token, userID = doc.AccessToken, doc.UserID.String()
	if token == "" && len(doc.Data) > 0 {
		token, userID = doc.Data[0].AccessToken, doc.Data[0].UserID.String()
	}
	if token == "" {
		return "", "", errors.New("Instagram did not return an access token")
	}
	return token, userID, nil
}

func getLongLived(rawURL string) (longLived, error) {
	res, err := fetch("GET", rawURL, nil, "")
	if err != nil {
		return longLived{}, err
	}
	if !ok2xx(res.Status) {
		return longLived{}, apiError(res.Status, res.Body)
	}
	var l longLived
	if json.Unmarshal([]byte(res.Body), &l) != nil || l.AccessToken == "" {
		return longLived{}, errors.New("Instagram did not return an access token")
	}
	return l, nil
}

func newToken(l longLived, userID, username string) igToken {
	secs := l.ExpiresIn
	if secs <= 0 {
		secs = defaultLongLived
	}
	t := now().UTC()
	return igToken{AccessToken: l.AccessToken, UserID: userID, Username: username, ObtainedAt: t, ExpiresAt: t.Add(time.Duration(secs) * time.Second)}
}

// lookupAccount reads the account's id and name; failures only mean no name.
func lookupAccount(token string) (id, username string) {
	q := url.Values{}
	q.Set("fields", "user_id,username")
	q.Set("access_token", token)
	res, err := fetch("GET", graphURL+"me?"+q.Encode(), nil, "")
	if err != nil || !ok2xx(res.Status) {
		return "", ""
	}
	var me struct {
		UserID   json.Number `json:"user_id"`
		Username string      `json:"username"`
	}
	if json.Unmarshal([]byte(res.Body), &me) != nil {
		return "", ""
	}
	return me.UserID.String(), strings.TrimSpace(me.Username)
}

// runTokenRefresh is the token cron: it renews the stored token when due.
func runTokenRefresh() {
	budgetEnd = now().Add(refreshBudget)
	defer func() { budgetEnd = time.Time{} }()
	tok, err := loadToken()
	if err != nil {
		logf("warn", "token renewal: could not read the connection")
		return
	}
	if tok != nil {
		renewToken(tok)
	}
}

// renewToken renews the long-lived token once it is a day old (Instagram
// refuses younger ones) or close to expiry, and returns the token to use from
// here on. On failure the old token stays. The refresh cron and Refresh now
// call it too, since the token cron's clock restarts whenever the plugin does.
func renewToken(tok *igToken) *igToken {
	young := now().Sub(tok.ObtainedAt) < minTokenAge
	nearExpiry := !tok.ExpiresAt.IsZero() && tok.ExpiresAt.Sub(now()) < renewWindow
	if young && !nearExpiry {
		return tok
	}
	s := loadSettings()
	q := url.Values{}
	q.Set("grant_type", "ig_refresh_token")
	q.Set("access_token", tok.AccessToken)
	l, err := getLongLived(graphURL + "refresh_access_token?" + q.Encode())
	if err == nil {
		fresh := newToken(l, tok.UserID, tok.Username)
		if err = saveToken(fresh); err == nil {
			return &fresh
		}
	}
	msg := redact("token renewal failed: "+err.Error(), tok.AccessToken, s.AppSecret)
	logf("warn", "%s", msg)
	if err := setStatus("instagram", msg, false); err != nil {
		logf("warn", "token renewal: recording the status failed")
	}
	return tok
}
