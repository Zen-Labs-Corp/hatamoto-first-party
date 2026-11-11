package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const testSecret = "s3cret-value"

// oauthEnv is a fake GitHub plus a fake host: http.fetch goes to the fake
// server, kv is a map, the database answers from canned rows and records writes.
type oauthEnv struct {
	t          *testing.T
	srv        *httptest.Server
	tokenBody  string
	userBody   string
	emailsBody string
	truncate   bool
	account    string // jsonb rows answering the account lookup
	kv         map[string]any
	logs       []string
	calls      []hostCall
	tokenForm  url.Values
	userAuth   string
}

func newOAuthEnv(t *testing.T) *oauthEnv {
	t.Helper()
	e := &oauthEnv{
		t:          t,
		tokenBody:  `{"access_token":"gho_tok","token_type":"bearer"}`,
		userBody:   `{"id":42,"login":"acme"}`,
		emailsBody: `[{"email":"Dev@Acme.io","verified":true,"primary":true},{"email":"x@acme.io","verified":false,"primary":false},{"email":"b@acme.io","verified":true,"primary":false}]`,
		account:    `[]`,
		kv:         map[string]any{"issued:st-1": true},
	}
	mux := http.NewServeMux()
	mux.HandleFunc("POST /login/oauth/access_token", func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		e.tokenForm, _ = url.ParseQuery(string(b))
		if r.Header.Get("Accept") != "application/json" {
			t.Error("token request without Accept: application/json")
		}
		io.WriteString(w, e.tokenBody)
	})
	mux.HandleFunc("GET /user", func(w http.ResponseWriter, r *http.Request) {
		e.userAuth = r.Header.Get("Authorization")
		if r.Header.Get("User-Agent") == "" {
			t.Error("user request without User-Agent")
		}
		io.WriteString(w, e.userBody)
	})
	mux.HandleFunc("GET /user/emails", func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, e.emailsBody) })
	e.srv = httptest.NewServer(mux)
	t.Cleanup(e.srv.Close)

	oldConfig, oldHost := config, callHost
	t.Cleanup(func() { config, callHost = oldConfig, oldHost })
	config = func(k string) (string, bool) {
		return map[string]string{
			"github_authorize_url": e.srv.URL + "/login/oauth/authorize",
			"github_token_url":     e.srv.URL + "/login/oauth/access_token",
			"github_api_url":       e.srv.URL,
		}[k], true
	}
	callHost = e.host
	return e
}

func (e *oauthEnv) host(fn string, args, out any) error {
	c := hostCall{fn: fn}
	if m, ok := args.(map[string]any); ok {
		c.sql, _ = m["sql"].(string)
		c.args, _ = m["args"].([]any)
	}
	e.calls = append(e.calls, c)
	switch fn {
	case "settings.get":
		return json.Unmarshal([]byte(`{"github_client_id":"cid","github_client_secret":"`+testSecret+`","public_url":"https://hatamoto.dev"}`), out)
	case "log":
		e.logs = append(e.logs, args.(map[string]string)["message"])
	case "kv.get":
		v, ok := e.kv[args.(map[string]string)["key"]]
		b, _ := json.Marshal(map[string]any{"found": ok, "value": v})
		return json.Unmarshal(b, out)
	case "kv.set":
		m := args.(map[string]any)
		e.kv[m["key"].(string)] = m["value"]
		if m["ttl_seconds"] != stateTTL {
			e.t.Errorf("state marker ttl %v", m["ttl_seconds"])
		}
	case "kv.delete":
		delete(e.kv, args.(map[string]string)["key"])
	case "http.fetch":
		m := args.(map[string]any)
		if m["timeout_ms"] != fetchTimeoutMS {
			e.t.Errorf("fetch timeout %v", m["timeout_ms"])
		}
		req, _ := http.NewRequest(m["method"].(string), m["url"].(string), strings.NewReader(m["body"].(string)))
		for k, v := range m["headers"].(map[string]string) {
			req.Header.Set(k, v)
		}
		resp, err := http.DefaultClient.Do(req)
		if err != nil {
			return err
		}
		defer resp.Body.Close()
		b, _ := io.ReadAll(resp.Body)
		return json.Unmarshal(mustJSON(fetched{Status: resp.StatusCode, Body: string(b), Truncated: e.truncate}), out)
	case "db.query":
		if strings.Contains(c.sql, "FROM accounts WHERE github_id") {
			return cell(out, e.account)
		}
		return cell(out, `[]`)
	case "db.exec":
		return affected(out, 1)
	}
	return nil
}

func mustJSON(v any) []byte { b, _ := json.Marshal(v); return b }

func (e *oauthEnv) callback(query url.Values, cookies map[string]string) sdk.HTTPResponse {
	e.t.Helper()
	resp, err := serve(sdk.HTTPRequest{
		Method: "GET", Path: "/auth/callback", Pattern: "/auth/callback",
		Query: query, Cookies: cookies,
	})
	if err != nil {
		e.t.Fatal(err)
	}
	return resp
}

func (e *oauthEnv) goodCallback() sdk.HTTPResponse {
	return e.callback(url.Values{"state": {"st-1"}, "code": {"c0de"}}, map[string]string{"st": "st-1"})
}

func cookieNamed(r sdk.HTTPResponse, name string) (sdk.SetCookie, bool) {
	for _, c := range r.SetCookies {
		if c.Name == name {
			return c, true
		}
	}
	return sdk.SetCookie{}, false
}

func (e *oauthEnv) sessionInserts() []hostCall {
	var out []hostCall
	for _, c := range e.calls {
		if strings.HasPrefix(c.sql, "INSERT INTO sessions") {
			out = append(out, c)
		}
	}
	return out
}

// wantFailure checks a 303 to the portal with a flash code and no session.
func (e *oauthEnv) wantFailure(r sdk.HTTPResponse, code string) {
	e.t.Helper()
	if r.Status != 303 || r.Headers["Location"] != "/developers" {
		e.t.Fatalf("response %+v", r)
	}
	if c, _ := cookieNamed(r, "fl"); c.Value != code {
		e.t.Errorf("flash %q, want %q", c.Value, code)
	}
	if _, ok := cookieNamed(r, "sid"); ok {
		e.t.Error("a failed sign-in set sid")
	}
	if len(e.sessionInserts()) != 0 {
		e.t.Error("a failed sign-in stored a session")
	}
	if c, ok := cookieNamed(r, "st"); !ok || c.Value != "" {
		e.t.Errorf("st cookie not cleared: %+v", c)
	}
}

func TestAuthorizeURL(t *testing.T) {
	newOAuthEnv(t)
	u, err := url.Parse(authorizeURL(settings{ClientID: "cid", PublicURL: "https://hatamoto.dev"}, "abc"))
	if err != nil {
		t.Fatal(err)
	}
	q := u.Query()
	if q.Get("scope") != "read:user user:email" || q.Get("state") != "abc" || q.Get("client_id") != "cid" ||
		q.Get("redirect_uri") != "https://hatamoto.dev/p/devportal/auth/callback" {
		t.Errorf("query %v", q)
	}
}

func TestCallbackNewUserStartsSignup(t *testing.T) {
	e := newOAuthEnv(t)
	r := e.goodCallback()
	if r.Status != 303 || r.Headers["Location"] != "/developers" || r.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("response %+v", r)
	}
	sid, ok := cookieNamed(r, "sid")
	if !ok || sid.Value == "" || sid.MaxAge != 30*86400 {
		t.Fatalf("sid %+v", sid)
	}
	ins := e.sessionInserts()
	if len(ins) != 1 {
		t.Fatalf("%d session inserts", len(ins))
	}
	a := ins[0].args
	if a[0] != hashToken(sid.Value) || a[1] != (*int64)(nil) || a[3] != int64(42) || a[4] != "acme" {
		t.Errorf("session args %v", a)
	}
	// Verified emails only, lower-cased, primary first.
	if a[5] != `["dev@acme.io","b@acme.io"]` {
		t.Errorf("emails %v", a[5])
	}
	if c, _ := cookieNamed(r, "st"); c.Value != "" || c.MaxAge != 1 {
		t.Errorf("st %+v", c)
	}
	if e.tokenForm.Get("code") != "c0de" || e.tokenForm.Get("client_secret") != testSecret ||
		e.tokenForm.Get("redirect_uri") != "https://hatamoto.dev/p/devportal/auth/callback" {
		t.Errorf("token request %v", e.tokenForm)
	}
	if e.userAuth != "Bearer gho_tok" {
		t.Errorf("user auth %q", e.userAuth)
	}
	if _, left := e.kv["issued:st-1"]; left {
		t.Error("state not spent")
	}
	for _, l := range e.logs {
		if strings.Contains(l, testSecret) || strings.Contains(l, "gho_tok") || strings.Contains(l, "c0de") {
			t.Errorf("log leaks: %s", l)
		}
	}
	// No account row and no token stored anywhere.
	for _, c := range e.calls {
		if strings.HasPrefix(c.sql, "INSERT INTO accounts") {
			t.Error("account created before the terms step")
		}
		for _, v := range c.args {
			if v == "gho_tok" {
				t.Error("access token stored")
			}
		}
	}
}

func TestCallbackExistingUserWithChangedLogin(t *testing.T) {
	e := newOAuthEnv(t)
	e.account = `[{"id":9,"status":"active","login":"old-name"}]`
	r := e.goodCallback()
	sid, ok := cookieNamed(r, "sid")
	if !ok {
		t.Fatalf("no session: %+v", r)
	}
	ins := e.sessionInserts()
	acct, _ := ins[0].args[1].(*int64)
	if len(ins) != 1 || acct == nil || *acct != 9 || ins[0].args[0] != hashToken(sid.Value) {
		t.Errorf("session %v", ins)
	}
	refreshed := false
	for _, c := range e.calls {
		if strings.HasPrefix(c.sql, "UPDATE accounts SET login") && c.args[1] == "acme" {
			refreshed = true
		}
	}
	if !refreshed {
		t.Error("login not refreshed")
	}
}

func TestCallbackSuspendedUserGetsNoSession(t *testing.T) {
	e := newOAuthEnv(t)
	e.account = `[{"id":9,"status":"suspended","login":"acme"}]`
	e.wantFailure(e.goodCallback(), "suspended")
}

func TestCallbackNoVerifiedEmail(t *testing.T) {
	e := newOAuthEnv(t)
	e.emailsBody = `[{"email":"x@acme.io","verified":false,"primary":true}]`
	e.wantFailure(e.goodCallback(), "noemail")
	// An existing account does not need one.
	e = newOAuthEnv(t)
	e.emailsBody = `[]`
	e.account = `[{"id":9,"status":"active","login":"acme"}]`
	if _, ok := cookieNamed(e.goodCallback(), "sid"); !ok {
		t.Error("existing account refused without an email")
	}
}

func TestCallbackRefusesBadState(t *testing.T) {
	cases := map[string]struct {
		query   url.Values
		cookies map[string]string
	}{
		"forged":         {url.Values{"state": {"forged"}, "code": {"c"}}, map[string]string{"st": "st-1"}},
		"no cookie":      {url.Values{"state": {"st-1"}, "code": {"c"}}, nil},
		"empty cookie":   {url.Values{"state": {"st-1"}, "code": {"c"}}, map[string]string{"st": ""}},
		"no state":       {url.Values{"code": {"c"}}, map[string]string{"st": "st-1"}},
		"too long":       {url.Values{"state": {strings.Repeat("a", 65)}, "code": {"c"}}, map[string]string{"st": strings.Repeat("a", 65)}},
		"empty vs empty": {url.Values{"state": {""}, "code": {"c"}}, map[string]string{"st": ""}},
	}
	for name, tc := range cases {
		e := newOAuthEnv(t)
		r := e.callback(tc.query, tc.cookies)
		if r.Status != 303 {
			t.Fatalf("%s: %+v", name, r)
		}
		if c, _ := cookieNamed(r, "fl"); c.Value != "state" {
			t.Errorf("%s: flash %q", name, c.Value)
		}
		if _, ok := cookieNamed(r, "sid"); ok || e.tokenForm != nil {
			t.Errorf("%s: went on after a bad state", name)
		}
	}
}

func TestCallbackRefusesAStateTheServerNeverIssued(t *testing.T) {
	e := newOAuthEnv(t)
	e.kv = map[string]any{}
	e.wantFailure(e.callback(url.Values{"state": {"planted"}, "code": {"c"}}, map[string]string{"st": "planted"}), "state")
	if e.tokenForm != nil {
		t.Error("a planted state reached GitHub")
	}
}

func TestCallbackStateIsSingleUse(t *testing.T) {
	e := newOAuthEnv(t)
	if _, ok := cookieNamed(e.goodCallback(), "sid"); !ok {
		t.Fatal("first use failed")
	}
	e.tokenForm = nil
	e.calls = nil
	e.wantFailure(e.goodCallback(), "state")
	if e.tokenForm != nil {
		t.Error("a replayed state reached GitHub")
	}
}

func TestCallbackDenied(t *testing.T) {
	for name, q := range map[string]url.Values{
		"error":   {"state": {"st-1"}, "error": {"access_denied"}},
		"no code": {"state": {"st-1"}},
	} {
		e := newOAuthEnv(t)
		e.wantFailure(e.callback(q, map[string]string{"st": "st-1"}), "denied")
		_ = name
	}
}

func TestCallbackGitHubFailures(t *testing.T) {
	cases := map[string]func(e *oauthEnv){
		"bad verification code": func(e *oauthEnv) { e.tokenBody = `{"error":"bad_verification_code"}` },
		"token not json":        func(e *oauthEnv) { e.tokenBody = `<html>` },
		"empty token":           func(e *oauthEnv) { e.tokenBody = `{}` },
		"truncated":             func(e *oauthEnv) { e.truncate = true },
		"user without id":       func(e *oauthEnv) { e.userBody = `{"login":"acme"}` },
		"user not json":         func(e *oauthEnv) { e.userBody = `nope` },
		"emails not json":       func(e *oauthEnv) { e.emailsBody = `{"message":"Bad credentials"}` },
	}
	for name, mutate := range cases {
		e := newOAuthEnv(t)
		mutate(e)
		e.wantFailure(e.goodCallback(), "failed")
		for _, l := range e.logs {
			if strings.Contains(l, testSecret) || strings.Contains(l, "gho_tok") || strings.Contains(l, "bad_verification") {
				t.Errorf("%s: log leaks: %s", name, l)
			}
		}
		if len(e.logs) != 1 || !strings.HasPrefix(e.logs[0], "callback failed: ") {
			t.Errorf("%s: logs %v", name, e.logs)
		}
	}
}

func TestCallbackGitHubErrorStatus(t *testing.T) {
	e := newOAuthEnv(t)
	e.srv.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, `{"message":"boom"}`, http.StatusBadGateway)
	})
	e.wantFailure(e.goodCallback(), "failed")
}

func TestCallbackNeedsConfiguredSettings(t *testing.T) {
	e := newOAuthEnv(t)
	callHost = func(fn string, args, out any) error {
		if fn == "settings.get" {
			return json.Unmarshal([]byte(`{"github_client_id":"","public_url":"https://hatamoto.dev"}`), out)
		}
		return e.host(fn, args, out)
	}
	e.wantFailure(e.goodCallback(), "failed")
}
