package accounts

import (
	"errors"
	"net/url"
	"strconv"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

func pwForm(current, pw string) url.Values {
	return url.Values{"current": {current}, "password": {pw}}
}

// The password routes run the member gate (keyed on the member id), never the login gate,
// so other people's login attempts cannot block a member's own profile forms.
func TestPasswordRoutesUseTheMemberGate(t *testing.T) {
	for _, route := range []string{"/password", "/password/connect"} {
		f := loginFixture(t)
		var got []*core.GateReq
		r := newGateRegistry(core.GateMember, func(g *core.GateReq) error { got = append(got, g); return gateRefusal{} })
		key, user := "ck-acc-ann", "Ann"
		if route == "/password/connect" {
			key, user = "k-eve", "Eve"
		}
		resp := postFrom(r, route, key, "same-origin", pwForm(annPw, goodPw))
		if resp.CommenterSession != nil || len(f.revoked) != 0 {
			t.Fatalf("%s: refusal still acted: %+v", route, resp)
		}
		if len(got) != 1 || got[0].Kind != core.GateMember || got[0].Target != strconv.FormatInt(f.byKey[key].ID, 10) {
			t.Fatalf("%s (%s): gate calls %+v", route, user, got)
		}
		login := newGateRegistry(core.GateLogin, func(*core.GateReq) error { t.Fatalf("%s ran the login gate", route); return nil })
		postFrom(login, route, key, "same-origin", pwForm(annPw, goodPw))
	}
}

func TestPasswordChangeNeedsTheRevoke(t *testing.T) {
	f := loginFixture(t)
	old := f.logins[f.byKey["ck-acc-ann"].ID].PwHash
	f.revokeErr = errors.New("rate_limited")
	resp := postFrom(newRegistry(nil), "/password", "ck-acc-ann", "same-origin", pwForm(annPw, "another horse"))
	if resp.Headers["Location"] != "/user?id=Ann&m=try_again" || resp.CommenterSession != nil {
		t.Fatalf("got %+v", resp)
	}
	if f.logins[f.byKey["ck-acc-ann"].ID].PwHash != old {
		t.Fatal("password changed although the other browsers could not be signed out")
	}
	f.revokeErr = nil
	resp = postFrom(newRegistry(nil), "/password", "ck-acc-ann", "same-origin", pwForm(annPw, "another horse"))
	if resp.Headers["Location"] != "/user?id=Ann&m=pw_saved" || len(f.revoked) != 2 {
		t.Fatalf("got %+v revoked %v", resp, f.revoked)
	}
}

// Connecting a password is permanent: it needs the member's username and the password twice.
func TestConnectNeedsConfirmation(t *testing.T) {
	for name, form := range map[string]url.Values{
		"nothing typed":    {"password": {goodPw}},
		"no repeat":        {"password": {goodPw}, "confirm": {"Eve"}},
		"repeat differs":   {"password": {goodPw}, "password2": {goodPw + "x"}, "confirm": {"Eve"}},
		"other username":   {"password": {goodPw}, "password2": {goodPw}, "confirm": {"Ann"}},
		"username omitted": {"password": {goodPw}, "password2": {goodPw}},
	} {
		f := loginFixture(t)
		resp := postFrom(newRegistry(nil), "/password/connect", "k-eve", "same-origin", form)
		if resp.Headers["Location"] != "/user?id=Eve&m=pw_confirm" || resp.CommenterSession != nil || f.logins[f.byKey["k-eve"].ID] != nil {
			t.Errorf("%s: got %+v", name, resp)
		}
	}
	f := loginFixture(t)
	resp := postFrom(newRegistry(nil), "/password/connect", "k-eve", "same-origin", url.Values{"password": {goodPw}, "password2": {goodPw}, "confirm": {" eve "}})
	if resp.CommenterSession == nil || !resp.CommenterSession.Link || f.logins[f.byKey["k-eve"].ID] == nil {
		t.Fatalf("confirmed connect: got %+v", resp)
	}
}
