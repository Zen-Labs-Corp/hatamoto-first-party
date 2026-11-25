package accounts

import (
	"net/url"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// postFrom posts to a route with the given Sec-Fetch-Site ("" = header absent).
func postFrom(r *core.Registry, route, key, site string, form url.Values) sdk.HTTPResponse {
	h := map[string]string{}
	if site != "" {
		h["sec-fetch-site"] = site
	}
	resp, _ := r.ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: route, Body: form.Encode(), CommenterKey: key, Headers: h})
	return resp
}

// newGateRegistry registers the phases with one gate of the given kind.
func newGateRegistry(kind core.GateKind, gate func(*core.GateReq) error) *core.Registry {
	r := core.NewRegistry()
	r.Register("accounts", Register)
	r.Register("stories", func(*core.Registry) {})
	r.Register("comments", func(*core.Registry) {})
	r.Register("moderation", func(r *core.Registry) { r.Gate(kind, gate) })
	return r
}

func TestSessionRouteOnlySameOrigin(t *testing.T) {
	for site, want := range map[string]int{"": 403, "none": 403, "same-site": 403, "cross-site": 403, "same-origin": 200} {
		called := false
		h := sessionRoute(func(*core.Request) (sdk.HTTPResponse, error) {
			called = true
			return sdk.HTTPResponse{Status: 200}, nil
		})
		req := core.NewRequest(sdk.HTTPRequest{Headers: map[string]string{"sec-fetch-site": site}})
		resp, err := h(req)
		if err != nil || resp.Status != want || called != (want == 200) {
			t.Errorf("site %q: status %d called %v err %v", site, resp.Status, called, err)
		}
	}
}

func TestSessionRouteThroughServeHTTP(t *testing.T) {
	f := installFake(t)
	form := url.Values{"username": {"ann"}, "password": {"correct horse"}}
	for _, site := range []string{"same-site", "", "none", "cross-site"} {
		resp := postFrom(newRegistry(nil), "/signup", "", site, form)
		if resp.Status != 403 || resp.CommenterSession != nil {
			t.Errorf("site %q: %d session %v", site, resp.Status, resp.CommenterSession)
		}
	}
	if len(f.ensured) != 0 || f.inserts != 0 {
		t.Fatalf("side effects on refused requests: %v %d", f.ensured, f.inserts)
	}
}
