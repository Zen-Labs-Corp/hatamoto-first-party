package accounts

import (
	"errors"
	"net/url"
	"testing"

	"hatamoto.dev/sdk"
)

var signedOut = sdk.CommenterSession{SignOut: true}

func TestLogoutClearsTheSession(t *testing.T) {
	f := installFake(t)
	resp := post(newRegistry(nil), "/logout", "", "")
	if resp.Status != 303 || resp.Headers["Location"] != "/" || resp.CommenterSession == nil || *resp.CommenterSession != signedOut {
		t.Fatalf("got %+v", resp)
	}
	if len(f.host.Calls) != 0 {
		t.Errorf("host calls %v", f.host.Calls)
	}
}

func TestLogoutAll(t *testing.T) {
	t.Run("revokes then signs out", func(t *testing.T) {
		f := loginFixture(t)
		resp := post(newRegistry(nil), "/logout/all", "ck-acc-ann", "")
		if resp.Status != 303 || resp.Headers["Location"] != "/" || resp.CommenterSession == nil || *resp.CommenterSession != signedOut {
			t.Fatalf("got %+v", resp)
		}
		if len(f.revoked) != 1 || f.revoked[0] != "acc-ann" {
			t.Fatalf("revoked %v", f.revoked)
		}
	})
	t.Run("revoked false still signs out", func(t *testing.T) {
		f := loginFixture(t)
		no := false
		f.revokeAnswer = &no
		resp := post(newRegistry(nil), "/logout/all", "ck-acc-ann", "")
		if resp.Headers["Location"] != "/" || resp.CommenterSession == nil || *resp.CommenterSession != signedOut {
			t.Fatalf("got %+v", resp)
		}
	})
	t.Run("no viewer", func(t *testing.T) {
		f := loginFixture(t)
		resp := post(newRegistry(nil), "/logout/all", "", "")
		if resp.Headers["Location"] != "/login?m=signin_first" || len(f.revoked) != 0 || resp.CommenterSession != nil {
			t.Fatalf("got %+v", resp)
		}
	})
	t.Run("member without login", func(t *testing.T) {
		f := loginFixture(t)
		resp := post(newRegistry(nil), "/logout/all", "k-eve", "")
		if resp.Headers["Location"] != "/user?id=Eve&m=try_again" || len(f.revoked) != 0 || resp.CommenterSession != nil {
			t.Fatalf("got %+v", resp)
		}
	})
	t.Run("revoke error", func(t *testing.T) {
		f := loginFixture(t)
		f.revokeErr = errors.New("unavailable")
		resp := post(newRegistry(nil), "/logout/all", "ck-acc-ann", "")
		if resp.Headers["Location"] != "/user?id=Ann&m=try_again" || resp.CommenterSession != nil {
			t.Fatalf("got %+v", resp)
		}
	})
}

func TestLogoutRoutesRefuseOtherOrigins(t *testing.T) {
	f := loginFixture(t)
	for _, route := range []string{"/logout", "/logout/all"} {
		for _, site := range []string{"same-site", "", "cross-site"} {
			resp := postFrom(newRegistry(nil), route, "ck-acc-ann", site, url.Values{})
			if resp.Status != 403 || resp.CommenterSession != nil {
				t.Errorf("%s from %q: %d", route, site, resp.Status)
			}
		}
	}
	if len(f.revoked) != 0 {
		t.Errorf("revoked %v", f.revoked)
	}
}
