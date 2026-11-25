package accounts

import (
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
