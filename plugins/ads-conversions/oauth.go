package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
	"time"
)

const (
	googleAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	googleTokenURL    = "https://oauth2.googleapis.com/token"
	googleRevokeURL   = "https://oauth2.googleapis.com/revoke"
	googleScope       = "https://www.googleapis.com/auth/datamanager"
	linkedInAuthURL   = "https://www.linkedin.com/oauth/v2/authorization"
	linkedInTokenURL  = "https://www.linkedin.com/oauth/v2/accessToken"
	linkedInScope     = "rw_conversions r_ads"
	refreshMargin     = 2 * time.Minute
	tokenKeyPrefix    = "token:"
	statusKeyPrefix   = "status:"
	alertedKeyPrefix  = "alerted:"
	statusReconnect   = "reconnect"
	linkedInExpiryKey = "alerted:linkedin-expiry"
)

var (
	// errNotConnected means no token is stored yet.
	errNotConnected = errors.New("not connected")
	// errReconnect means the connection is broken and the owner must connect
	// again: the grant was revoked, or the stored token cannot be read.
	errReconnect = errors.New("the connection needs reconnecting")
	// errInvalidGrant is the token endpoint refusing a refresh or a code.
	errInvalidGrant = errors.New("the platform refused the grant")
)

// platformLabel is how the platform is named to the owner.
var platformLabel = map[string]string{platGoogle: "Google Ads", platTikTok: "TikTok", platLinkedIn: "LinkedIn"}

// tokenSet is what is kept per OAuth platform. Expires is zero when the
// platform gave no lifetime.
type tokenSet struct {
	Access  string    `json:"access"`
	Refresh string    `json:"refresh,omitempty"`
	Expires time.Time `json:"expires"`
}

// clientCreds returns the OAuth client of an OAuth platform.
func clientCreds(s settings, platform string) (id, secret string) {
	switch platform {
	case platGoogle:
		return s.Google.ClientID, s.Google.ClientSecret
	case platLinkedIn:
		return s.LinkedIn.ClientID, s.LinkedIn.ClientSecret
	}
	return "", ""
}

// saveTokens stores the sealed token set in kv, without expiry.
func saveTokens(s settings, platform string, t tokenSet) error {
	_, secret := clientCreds(s, platform)
	key, err := deriveKey(secret)
	if err != nil {
		return err
	}
	b, err := json.Marshal(t)
	if err != nil {
		return err
	}
	sealed, err := seal(key, string(b))
	if err != nil {
		return err
	}
	return kvSet(tokenKeyPrefix+platform, sealed, 0)
}

// loadTokens reads the stored token set. A token that cannot be opened (the
// client secret changed) is errReconnect.
func loadTokens(s settings, platform string) (tokenSet, error) {
	var sealed string
	found, err := kvGet(tokenKeyPrefix+platform, &sealed)
	if err != nil {
		return tokenSet{}, err
	}
	if !found {
		return tokenSet{}, errNotConnected
	}
	_, secret := clientCreds(s, platform)
	key, err := deriveKey(secret)
	if err != nil {
		return tokenSet{}, errReconnect
	}
	plain, err := open(key, sealed)
	if err != nil {
		return tokenSet{}, errReconnect
	}
	var t tokenSet
	if json.Unmarshal([]byte(plain), &t) != nil || t.Access == "" {
		return tokenSet{}, errReconnect
	}
	return t, nil
}

// authorizationURL is the platform's consent page address. Google gets PKCE
// and offline access; LinkedIn gets the conversion scopes.
func authorizationURL(s settings, platform, state, challenge string) string {
	id, _ := clientCreds(s, platform)
	q := url.Values{"response_type": {"code"}, "client_id": {id}, "redirect_uri": {s.redirectURI(platform)}, "state": {state}}
	if platform == platGoogle {
		q.Set("scope", googleScope)
		q.Set("access_type", "offline")
		q.Set("prompt", "consent")
		q.Set("code_challenge", challenge)
		q.Set("code_challenge_method", "S256")
		return googleAuthURL + "?" + q.Encode()
	}
	q.Set("scope", linkedInScope)
	return linkedInAuthURL + "?" + q.Encode()
}

type tokenAnswer struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Error        string `json:"error"`
}

// tokenRequest posts a form to the platform's token endpoint. The answer body
// is never logged or returned: only errInvalidGrant or a status-only error.
func tokenRequest(s settings, platform string, form url.Values) (tokenAnswer, error) {
	id, secret := clientCreds(s, platform)
	form.Set("client_id", id)
	form.Set("client_secret", secret)
	endpoint := googleTokenURL
	if platform == platLinkedIn {
		endpoint = linkedInTokenURL
	}
	f, err := httpDo(request{Method: "POST", URL: endpoint, Body: []byte(form.Encode()),
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}}, callTimeout())
	if err != nil {
		return tokenAnswer{}, errors.New("token endpoint unreachable: " + hostReason(err))
	}
	var a tokenAnswer
	_ = json.Unmarshal([]byte(f.Body), &a)
	if f.Status == 400 && a.Error == "invalid_grant" {
		return a, errInvalidGrant
	}
	if f.Status != 200 || f.Truncated || a.AccessToken == "" {
		return a, errors.New("token endpoint answered HTTP " + strconv.Itoa(f.Status))
	}
	return a, nil
}

func (a tokenAnswer) tokens(prevRefresh string) tokenSet {
	t := tokenSet{Access: a.AccessToken, Refresh: a.RefreshToken}
	if t.Refresh == "" {
		t.Refresh = prevRefresh
	}
	if a.ExpiresIn > 0 {
		t.Expires = now().Add(time.Duration(a.ExpiresIn) * time.Second)
	}
	return t
}

// exchangeCode turns the one-time code from the callback into tokens. Google
// must grant offline access (a refresh token); LinkedIn may not.
func exchangeCode(s settings, platform, code, verifier string) (tokenSet, error) {
	form := url.Values{"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.redirectURI(platform)}}
	if platform == platGoogle {
		form.Set("code_verifier", verifier)
	}
	a, err := tokenRequest(s, platform, form)
	if err != nil {
		return tokenSet{}, err
	}
	t := a.tokens("")
	if platform == platGoogle && t.Refresh == "" {
		return tokenSet{}, errors.New("no refresh token granted")
	}
	return t, nil
}

// refreshTokens swaps the refresh token for a new access token and saves it.
// invalid_grant breaks the connection.
func refreshTokens(s settings, platform string, cur tokenSet) (tokenSet, error) {
	a, err := tokenRequest(s, platform, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {cur.Refresh}})
	if errors.Is(err, errInvalidGrant) {
		markBroken(s, platform)
		return tokenSet{}, errReconnect
	}
	if err != nil {
		return tokenSet{}, err
	}
	t := a.tokens(cur.Refresh)
	if err := saveTokens(s, platform, t); err != nil {
		return tokenSet{}, err
	}
	return t, nil
}

// accessToken returns a usable access token. It refreshes when under two
// minutes are left, or when force is set (after a 401). A LinkedIn token
// without a refresh token is used until it expires; when it cannot be renewed
// the connection is marked broken.
func accessToken(s settings, platform string, force bool) (string, error) {
	t, err := loadTokens(s, platform)
	if err != nil {
		return "", err
	}
	fresh := t.Expires.IsZero() || t.Expires.Sub(now()) > refreshMargin
	if !force && fresh {
		return t.Access, nil
	}
	if t.Refresh == "" {
		if !force && now().Before(t.Expires) {
			return t.Access, nil
		}
		markBroken(s, platform)
		return "", errReconnect
	}
	t, err = refreshTokens(s, platform, t)
	if err != nil {
		return "", err
	}
	return t.Access, nil
}

// tokenFingerprint names an access token without keeping it: the first 16 hex
// characters of its SHA-256.
func tokenFingerprint(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])[:16]
}

// statusValue is what the platform's status key holds while it is broken.
// TikTok has no reconnect: its broken flag is bound to the access token that
// was refused, so pasting a new token (or a passing Test) lifts it.
func statusValue(s settings, platform string) string {
	if platform == platTikTok {
		return tokenFingerprint(s.TikTok.AccessToken)
	}
	return statusReconnect
}

// alertKey is the "alert sent" flag of the platform; TikTok's is per token, so
// a second refused token is mailed again.
func alertKey(s settings, platform string) string {
	if platform == platTikTok {
		return alertedKeyPrefix + platform + ":" + tokenFingerprint(s.TikTok.AccessToken)
	}
	return alertedKeyPrefix + platform
}

// markBroken stops the platform and tells the owner once. The alert flag is
// set only after a mail went out, so a mail that was refused is tried again
// by a later run.
func markBroken(s settings, platform string) {
	if err := kvSet(statusKeyPrefix+platform, statusValue(s, platform), 0); err != nil {
		logf("warn", "ads-conversions: could not mark %s as needing a reconnect", platform)
	}
	text := "The " + platformLabel[platform] + " connection of the server-side conversions plugin stopped working, so no conversions are sent to it.\n" +
		"Open the plugin page in the admin area and choose Connect (or update the access token in the settings).\n"
	if platform == platTikTok {
		text = "TikTok refused the access token of the server-side conversions plugin, so no conversions are sent to it.\n" +
			"Create a new access token in Events Manager, enter it in the plugin settings and press Test on the plugin page.\n"
	}
	notifyOnce(s, alertKey(s, platform), platformLabel[platform]+" needs reconnecting", text)
}

// notifyOnce mails notify_email unless the flag key says it already did. Mail
// text holds no value, hash, click ID or token.
func notifyOnce(s settings, flag, subject, text string) {
	if s.NotifyEmail == "" {
		return
	}
	var sent bool
	if found, err := kvGet(flag, &sent); err != nil || found {
		return
	}
	err := callHost("mail.send", map[string]any{"to": []string{s.NotifyEmail}, "subject": subject, "text": text}, nil)
	if err != nil {
		logf("warn", "ads-conversions: alert email failed: %s", hostReason(err))
		return
	}
	if err := kvSet(flag, true, 0); err != nil {
		logf("warn", "ads-conversions: could not remember the alert")
	}
}

// clearBroken forgets the broken state and the alert flags after a connect or
// a disconnect.
func clearBroken(platform string) {
	for _, k := range []string{statusKeyPrefix + platform, alertedKeyPrefix + platform} {
		_ = kvDelete(k)
	}
	if platform == platLinkedIn {
		_ = kvDelete(linkedInExpiryKey)
	}
}

// clearTikTokBroken lifts TikTok's broken state for the current token.
func clearTikTokBroken(s settings) {
	for _, k := range []string{statusKeyPrefix + platTikTok, alertKey(s, platTikTok)} {
		_ = kvDelete(k)
	}
}

// revokeGoogle asks Google to forget the grant. It is best effort.
func revokeGoogle(token string) {
	_, _ = httpDo(request{Method: "POST", URL: googleRevokeURL, Body: []byte(url.Values{"token": {token}}.Encode()),
		Headers: map[string]string{"Content-Type": "application/x-www-form-urlencoded"}}, callTimeout())
}
