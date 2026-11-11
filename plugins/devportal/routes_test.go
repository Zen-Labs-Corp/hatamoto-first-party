package main

import (
	"os"
	"regexp"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
)

// Every manifest route has a case in serve and every case is in the manifest.
func TestServeCasesMatchTheManifestRoutes(t *testing.T) {
	src, err := os.ReadFile("routes.go")
	if err != nil {
		t.Fatal(err)
	}
	cases := map[string]bool{}
	for _, m := range regexp.MustCompile(`(?m)^\tcase "([A-Z]+ [^"]+)":`).FindAllStringSubmatch(string(src), -1) {
		cases[m[1]] = true
	}
	routes := map[string]bool{}
	for _, r := range loadManifest(t).Routes {
		routes[r.Method+" "+r.Path] = true
	}
	for k := range routes {
		if !cases[k] {
			t.Errorf("manifest route %s has no serve case", k)
		}
	}
	for k := range cases {
		if !routes[k] {
			t.Errorf("serve case %s is not in the manifest", k)
		}
	}
}

// Every manifest route reaches a handler, not the router's own 404.
func TestServeReachesAHandlerForEveryRoute(t *testing.T) {
	(&fakeHost{settings: `{}`, db: func(hostCall, any) error { return errDB }}).install(t)
	for _, r := range loadManifest(t).Routes {
		req := sdk.HTTPRequest{Method: r.Method, Path: r.Path, Pattern: r.Path, PathValues: map[string]string{"id": "1"},
			User: &sdk.HTTPUser{ID: "u", Role: "owner"}}
		resp, err := serve(req)
		if err != nil {
			t.Fatal(err)
		}
		if resp.Status == 404 && strings.Contains(resp.Body, `"not found"`) {
			t.Errorf("%s %s fell through serve", r.Method, r.Path)
		}
	}
	if resp, _ := serve(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"}); resp.Status != 404 {
		t.Errorf("unknown route answered %d", resp.Status)
	}
}

func TestServeRegistryJSONAndSubmit(t *testing.T) {
	h := newIndexHost(t, "")
	h.entries = `[{"body":"{\"index\":{}}"}]`
	resp, err := serve(sdk.HTTPRequest{Method: "GET", Path: "/registry.json", Pattern: "/registry.json"})
	if err != nil || resp.Status != 200 || resp.Body != `{"index":{}}` {
		t.Fatalf("registry: %+v %v", resp, err)
	}
	// A submission from a foreign origin is refused by the shared form guard.
	resp, err = serve(post("/submit", "https://evil.example", urlenc, "x=1", map[string]string{"sid": "tok"}))
	if err != nil || resp.Status != 403 {
		t.Fatalf("submit: %+v %v", resp, err)
	}
}

func TestSubmitRouteAcceptsAGoodSubmission(t *testing.T) {
	newSubEnv(t, "active")
	resp, err := serve(post("/submit", "https://hatamoto.dev", urlenc, submitBody(), map[string]string{"sid": "tok"}))
	if err != nil || resp.Status != 303 {
		t.Fatalf("submit: %+v %v", resp, err)
	}
}
