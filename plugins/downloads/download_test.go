package main

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const fileID = "6f1c0a9e-5b7d-4e28-a3c1-f09d7b2e4a65"

type hostCall struct {
	fn   string
	sql  string
	args []any
	raw  any
}

// fakeHost routes host calls to per-function answers and records every call.
type fakeHost struct {
	calls []hostCall
	reply map[string]func(args any, out any) error
}

func (f *fakeHost) install(t *testing.T) {
	t.Helper()
	callHost = func(fn string, a, out any) error {
		c := hostCall{fn: fn, raw: a}
		if m, ok := a.(map[string]any); ok {
			c.sql, _ = m["sql"].(string)
			c.args, _ = m["args"].([]any)
		}
		f.calls = append(f.calls, c)
		if h := f.reply[fn]; h != nil {
			return h(a, out)
		}
		return nil
	}
}

func (f *fakeHost) count(fn string) (n int) {
	for _, c := range f.calls {
		if c.fn == fn {
			n++
		}
	}
	return
}

func decode(t *testing.T, raw string, out any) error {
	t.Helper()
	return json.Unmarshal([]byte(raw), out)
}

// mediaHost serves one file with the given public_url JSON ("null" = absent).
func mediaHost(t *testing.T, publicURL string, settings string) *fakeHost {
	f := &fakeHost{reply: map[string]func(any, any) error{
		"media.get": func(_, out any) error {
			return decode(t, `{"id":"`+fileID+`","filename":"a.pdf","mime_type":"application/pdf","file_size":90112,"public_url":`+publicURL+`}`, out)
		},
		"settings.get": func(_, out any) error { return decode(t, settings, out) },
	}}
	f.install(t)
	return f
}

func get(t *testing.T, method, id, ua string) sdk.HTTPResponse {
	t.Helper()
	req := sdk.HTTPRequest{Method: method, Pattern: "/f/{media_id}", PathValues: map[string]string{"media_id": id}}
	if ua != "" {
		req.Headers = map[string]string{"user-agent": ua}
	}
	in, _ := json.Marshal(req)
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("handler returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

const browser = "Mozilla/5.0 (X11; Linux x86_64) Firefox/130.0"

func TestDownloadCountsAndRedirects(t *testing.T) {
	now = func() time.Time { return time.Date(2026, 11, 4, 23, 59, 0, 0, time.FixedZone("x", -5*3600)) }
	defer func() { now = time.Now }()
	f := mediaHost(t, `"/media/a.pdf"`, `{"count_bots":false}`)
	res := get(t, "GET", strings.ToUpper(fileID), browser)
	if res.Status != 302 || res.Headers["Location"] != "/media/a.pdf" || res.Headers["Cache-Control"] != "no-store" {
		t.Fatalf("res = %+v", res)
	}
	if f.count("db.exec") != 1 {
		t.Fatalf("calls = %+v", f.calls)
	}
	for _, c := range f.calls {
		if c.fn == "db.exec" && (c.args[0] != fileID || c.args[1] != "2026-11-05") {
			t.Fatalf("count args = %v", c.args)
		}
	}
}

func TestDownloadRefusesUnsafeLocations(t *testing.T) {
	for _, u := range []string{`"https://cdn.example/a.pdf"`, `"//x/a.pdf"`, `"/\\x"`, `"/a b.pdf"`, `"/a\u0001.pdf"`, `"/a\u007f.pdf"`, `"a.pdf"`, `""`, `null`} {
		f := mediaHost(t, u, `{}`)
		res := get(t, "GET", fileID, browser)
		if res.Status != 404 || res.Body != "not found" || res.Headers["Content-Type"] != "text/plain" || res.Headers["Location"] != "" {
			t.Errorf("%s: res = %+v", u, res)
		}
		if f.count("db.exec") != 0 {
			t.Errorf("%s: counted", u)
		}
	}
}

func TestDownloadBadAndUnknownIDs(t *testing.T) {
	for _, id := range []string{"", "abc", "../x", fileID + "0", strings.ReplaceAll(fileID, "-", ""), "g" + fileID[1:]} {
		f := mediaHost(t, `"/media/a.pdf"`, `{}`)
		if res := get(t, "GET", id, browser); res.Status != 404 {
			t.Errorf("%q: status %d", id, res.Status)
		}
		if len(f.calls) != 0 {
			t.Errorf("%q: host called: %+v", id, f.calls)
		}
	}
	f := &fakeHost{reply: map[string]func(any, any) error{
		"media.get": func(any, any) error { return &sdk.HostError{Code: sdk.ErrNotFound} },
	}}
	f.install(t)
	if res := get(t, "GET", fileID, browser); res.Status != 404 || f.count("db.exec") != 0 {
		t.Fatalf("not_found: %+v, calls %+v", res, f.calls)
	}
}

func TestDownloadHostFailureIsUnavailable(t *testing.T) {
	for _, err := range []error{errors.New("boom"), &sdk.HostError{Code: sdk.ErrInternal, Message: "not_found"}} {
		f := &fakeHost{reply: map[string]func(any, any) error{
			"media.get": func(any, any) error { return err },
		}}
		f.install(t)
		res := get(t, "GET", fileID, browser)
		if res.Status != 503 || res.Body != "unavailable" || res.Headers["Content-Type"] != "text/plain" || f.count("db.exec") != 0 {
			t.Errorf("%v: %+v", err, res)
		}
	}
}

func TestDownloadBots(t *testing.T) {
	uas := []string{"", "Googlebot/2.1", "MY-CRAWLER", "Baiduspider", "Yahoo! Slurp", "Slack-ImgProxy link preview", "facebookexternalhit/1.1",
		"curl/8.5", "Wget/1.21", "python-requests/2", "Go-http-client/2.0", "HeadlessChrome", "UptimeMonitor", "Nmap Scanner", "BOT"}
	for _, ua := range uas {
		for _, ua := range []string{ua, strings.ToLower(ua)} {
			f := mediaHost(t, `"/media/a.pdf"`, `{"count_bots":false}`)
			if res := get(t, "GET", fileID, ua); res.Status != 302 || f.count("db.exec") != 0 {
				t.Errorf("ua %q: status %d, calls %+v", ua, res.Status, f.calls)
			}
		}
	}
	f := mediaHost(t, `"/media/a.pdf"`, `{"count_bots":true}`)
	if res := get(t, "GET", fileID, "Googlebot/2.1"); res.Status != 302 || f.count("db.exec") != 1 {
		t.Fatalf("count_bots true: %+v %+v", res, f.calls)
	}
	f = mediaHost(t, `"/media/a.pdf"`, `{"count_bots":true}`)
	if res := get(t, "HEAD", fileID, browser); res.Status != 302 || res.Headers["Location"] != "/media/a.pdf" || f.count("db.exec") != 0 {
		t.Fatalf("HEAD: %+v %+v", res, f.calls)
	}
}

func TestDownloadFailsOpen(t *testing.T) {
	f := mediaHost(t, `"/media/a.pdf"`, `{}`)
	f.reply["db.exec"] = func(any, any) error { return errors.New("db down") }
	if res := get(t, "GET", fileID, browser); res.Status != 302 {
		t.Fatalf("db error: %+v", res)
	}
	f = mediaHost(t, `"/media/a.pdf"`, `{}`)
	f.reply["settings.get"] = func(any, any) error { return errors.New("settings down") }
	if res := get(t, "GET", fileID, "Googlebot"); res.Status != 302 || f.count("db.exec") != 0 {
		t.Fatalf("settings error, bot: %+v %+v", res, f.calls)
	}
	if res := get(t, "GET", fileID, browser); res.Status != 302 || f.count("db.exec") != 1 {
		t.Fatalf("settings error, browser: %+v %+v", res, f.calls)
	}
}
