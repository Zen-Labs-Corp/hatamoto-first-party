package main

import (
	"encoding/json"
	"errors"
	"net/url"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

const portalSettings = `{"github_client_id":"cid","github_client_secret":"shh","public_url":"https://hatamoto.dev","review_email":"review@example.org"}`

// portal is a fake host with an in-memory session table: enough database to
// run the page filter and the POST routes end to end.
type portal struct {
	t        *testing.T
	settings string
	sessions map[string]string // sha256 hex of the token -> session row JSON
	dbErr    error
	keyMine  int
	keyTaken int
	insertN  int // rows_affected of INSERT INTO keys
	account  string
	packages string // answer of the dashboard packages query
	calls    []hostCall
}

func newPortal(t *testing.T) *portal {
	t.Helper()
	p := &portal{t: t, settings: portalSettings, sessions: map[string]string{}, insertN: 1, account: `[]`, packages: `{"packages":[],"result":null}`}
	oldConfig, oldHost := config, callHost
	t.Cleanup(func() { config, callHost = oldConfig, oldHost })
	config = func(k string) (string, bool) {
		return map[string]string{"github_authorize_url": "https://github.com/login/oauth/authorize"}[k], true
	}
	callHost = p.host
	return p
}

func (p *portal) host(fn string, args, out any) error {
	c := hostCall{fn: fn}
	if m, ok := args.(map[string]any); ok {
		c.sql, _ = m["sql"].(string)
		c.args, _ = m["args"].([]any)
	}
	p.calls = append(p.calls, c)
	switch {
	case fn == "settings.get":
		return json.Unmarshal([]byte(p.settings), out)
	case fn == "log":
		return nil
	case strings.HasPrefix(fn, "db.") && p.dbErr != nil:
		return p.dbErr
	case fn == "db.query" && strings.Contains(c.sql, "FROM sessions s LEFT JOIN"):
		if row, ok := p.sessions[c.args[0].(string)]; ok {
			return cell(out, "["+row+"]")
		}
		return cell(out, `[]`)
	case fn == "db.query" && strings.Contains(c.sql, "'packages'"):
		return cell(out, p.packages)
	case fn == "db.query" && strings.Contains(c.sql, "SELECT (SELECT count(*) FROM keys"):
		return cell(out, `[{"mine":`+string(rune('0'+p.keyMine))+`,"taken":`+string(rune('0'+p.keyTaken))+`}]`)
	case fn == "db.query" && strings.Contains(c.sql, "FROM accounts WHERE github_id"):
		return cell(out, p.account)
	case fn == "db.exec" && strings.HasPrefix(c.sql, "DELETE FROM sessions WHERE id"):
		delete(p.sessions, c.args[0].(string))
		return affected(out, 1)
	case fn == "db.exec" && strings.HasPrefix(c.sql, "INSERT INTO keys"):
		return affected(out, p.insertN)
	case fn == "db.exec":
		return affected(out, 1)
	}
	return nil
}

// login registers a session row for a token.
func (p *portal) login(token, row string) { p.sessions[hashToken(token)] = row }

func (p *portal) execs(prefix string) []hostCall {
	var out []hostCall
	for _, c := range p.calls {
		if c.fn == "db.exec" && strings.HasPrefix(c.sql, prefix) {
			out = append(out, c)
		}
	}
	return out
}

const farFuture = `"expires_at":"2099-01-01T00:00:00+00:00"`

func signupRow(csrf string) string {
	return `{"id":"h","account":null,"csrf":"` + csrf + `","gh_id":42,"gh_login":"acme","gh_emails":["dev@acme.io","b@acme.io"],` + farFuture +
		`,"status":null,"login":null,"display_name":null,"email":null,"plan":null,"key_id":null,"key_added":null}`
}

func accountRow(csrf, status, keyID string) string {
	key := `"key_id":null,"key_added":null`
	if keyID != "" {
		key = `"key_id":"` + keyID + `","key_added":"2026-11-09T10:00:00+00:00"`
	}
	return `{"id":"h","account":7,"csrf":"` + csrf + `","gh_id":null,"gh_login":null,"gh_emails":null,` + farFuture +
		`,"status":"` + status + `","login":"acme","display_name":"Acme Ltd","email":"dev@acme.io","plan":"free",` + key + `}`
}

func form(kv ...string) string {
	v := url.Values{}
	for i := 0; i < len(kv); i += 2 {
		v.Set(kv[i], kv[i+1])
	}
	return v.Encode()
}

func devPage(cookies map[string]string) sdk.PageContext {
	return sdk.PageContext{
		Path:    "/developers",
		Cookies: cookies,
		Blocks: []sdk.Block{
			{ID: "b1", Type: "text", Title: "Developers", Fields: map[string]any{"anchor": "devportal"}},
		},
	}
}

func mustFilter(t *testing.T, p sdk.PageContext) sdk.PageContext {
	t.Helper()
	out, err := pageFilter(p)
	if err != nil {
		t.Fatal(err)
	}
	return out
}

var errDB = errors.New("db down")
