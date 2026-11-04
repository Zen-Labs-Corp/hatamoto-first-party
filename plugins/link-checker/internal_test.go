package main

import (
	"encoding/json"
	"errors"
	"testing"

	"hatamoto.dev/sdk"
)

// internalHost answers entities.get for the published paths, redirects.list
// with the given rules and known_paths with known.
func internalHost(t *testing.T, published map[string]bool, redirects string, known map[string]bool) *fakeHost {
	return newFakeHost(t, func(c call, out any) error {
		switch c.Fn {
		case "entities.get":
			if published[c.Args["path"].(string)] {
				return json.Unmarshal([]byte(`{"id":"e1","path":"/x"}`), out)
			}
			return &sdk.HostError{Code: sdk.ErrNotFound, Message: "entity not found"}
		case "redirects.list":
			return json.Unmarshal([]byte(redirects), out)
		case "db.query":
			return cell(out, `{"known":`+map[bool]string{true: "true", false: "false"}[known[c.params()[0].(string)]]+`}`)
		}
		t.Fatalf("unexpected host call %q", c.Fn)
		return nil
	})
}

func TestCheckInternal(t *testing.T) {
	published := map[string]bool{"/about": true, "/blog/post": true}
	redirects := `{"items":[{"source_path":"/old","is_active":true},{"source_path":"/off","is_active":false}],"total":2}`
	known := map[string]bool{"/blog": true}
	cases := []struct {
		path   string
		status string
		err    string
	}{
		{"/about", statusOK, ""},
		{"/about/", statusOK, ""},
		{"/about?utm=1", statusOK, ""},
		{"/about#team", statusOK, ""},
		{"/old", statusRedirected, ""},
		{"/off", statusBroken, "no page"},
		{"/", statusOK, ""},
		{"/blog", statusOK, ""},
		{"/files/report.pdf", statusOK, ""},
		{"/media/x", statusOK, ""},
		{"/uploads/2026/x", statusOK, ""},
		{"/p/plugin/page", statusOK, ""},
		{"/assets/app", statusOK, ""},
		{"/services/old", statusBroken, "no page"},
	}
	for _, c := range cases {
		f := internalHost(t, published, redirects, known)
		got := checkInternal(c.path, &redirectCache{})
		if got.Status != c.status || got.Err != c.err || got.Keep {
			t.Errorf("%s: got %+v want %s %q", c.path, got, c.status, c.err)
		}
		if f.count("http.fetch") != 0 {
			t.Errorf("%s: internal links must never use http.fetch", c.path)
		}
		for _, call := range f.calls {
			if call.Fn == "entities.get" {
				if p := call.Args["path"].(string); p != trimPath(c.path) || len(call.Args) != 1 {
					t.Errorf("%s: entities.get args %v", c.path, call.Args)
				}
			}
		}
	}
}

func TestRedirectCacheLoadsOncePerRunAndPages(t *testing.T) {
	var pages []float64
	f := newFakeHost(t, func(c call, out any) error {
		switch c.Fn {
		case "entities.get":
			return &sdk.HostError{Code: sdk.ErrNotFound}
		case "redirects.list":
			p := c.Args["page"].(float64)
			pages = append(pages, p)
			if c.Args["per_page"] != float64(200) {
				t.Fatalf("args %v", c.Args)
			}
			if p == 1 {
				return json.Unmarshal([]byte(`{"items":[{"source_path":"/a","is_active":true}],"total":201}`), out)
			}
			return json.Unmarshal([]byte(`{"items":[{"source_path":"/b/","is_active":true}],"total":201}`), out)
		}
		return nil
	})
	rc := &redirectCache{}
	for _, p := range []string{"/a", "/b", "/c.png"} {
		if r := checkInternal(p, rc); r.Status != statusRedirected && p != "/c.png" {
			t.Fatalf("%s: %+v", p, r)
		}
	}
	if len(pages) != 2 || f.count("http.fetch") != 0 {
		t.Fatalf("redirects.list pages %v", pages)
	}
}

func TestCheckInternalHostErrorsKeepState(t *testing.T) {
	for name, errs := range map[string]struct{ get, redirects, db error }{
		"capability denied": {get: &sdk.HostError{Code: sdk.ErrCapabilityDenied}},
		"unavailable":       {get: &sdk.HostError{Code: sdk.ErrUnavailable}},
		"plain error":       {get: errors.New("boom")},
		"redirects down":    {get: &sdk.HostError{Code: sdk.ErrNotFound}, redirects: &sdk.HostError{Code: sdk.ErrCapabilityDenied}},
		"db down":           {get: &sdk.HostError{Code: sdk.ErrNotFound}, db: errors.New("db")},
	} {
		f := newFakeHost(t, func(c call, out any) error {
			switch c.Fn {
			case "entities.get":
				return errs.get
			case "redirects.list":
				if errs.redirects != nil {
					return errs.redirects
				}
				return json.Unmarshal([]byte(`{"items":[],"total":0}`), out)
			case "db.query":
				return errs.db
			}
			return nil
		})
		got := checkInternal("/missing", &redirectCache{})
		if !got.Keep {
			t.Errorf("%s: want no state change, got %+v", name, got)
		}
		if f.count("http.fetch") != 0 || f.count("log") == 0 {
			t.Errorf("%s: no fetch and one log line expected, calls %v", name, f.calls)
		}
	}
}
