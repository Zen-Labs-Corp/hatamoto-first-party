package main

import (
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
)

const (
	sampleEmail = "John_Smith@example.com"
	samplePhone = "+61 (04) 1234-5678"
	sampleName  = "Mary Smith"
	sampleToken = "tok-SECRET-123"
)

var errHostDown = errors.New("host down")

type call struct {
	fn   string
	args string // JSON of the arguments, for the leak scan
	raw  any
}

type row struct {
	form, status, reason string
	code                 any
	message              any
	test                 bool
}

// host is a fake host that records every call and keeps the deliveries table.
type host struct {
	t           *testing.T
	settings    string
	settingsErr error
	calls       []call
	rows        map[string]*row
	fetchStatus int
	fetchBody   string
	fetchErr    error
	panicOn     string
}

func newHost(t *testing.T) *host {
	h := &host{t: t, rows: map[string]*row{}, fetchStatus: 200, fetchBody: `{"events_received":1}`}
	callHost = h.call
	return h
}

func (h *host) configured(extra ...string) {
	s := `{"pixel_id":"123456789","access_token":"` + sampleToken + `"`
	for _, e := range extra {
		s += "," + e
	}
	h.settings = s + "}"
}

func (h *host) count(fn string) int {
	n := 0
	for _, c := range h.calls {
		if c.fn == fn {
			n++
		}
	}
	return n
}

func (h *host) callsOf(fn string) []call {
	var out []call
	for _, c := range h.calls {
		if c.fn == fn {
			out = append(out, c)
		}
	}
	return out
}

func (h *host) call(fn string, args, out any) error {
	b, _ := json.Marshal(args)
	h.calls = append(h.calls, call{fn, string(b), args})
	if fn == h.panicOn {
		panic("boom")
	}
	switch fn {
	case "settings.get":
		if h.settingsErr != nil {
			return h.settingsErr
		}
		raw := h.settings
		if raw == "" {
			raw = "{}"
		}
		return json.Unmarshal([]byte(raw), out)
	case "db.exec":
		return h.dbExec(args.(map[string]any), out)
	case "http.fetch":
		if h.fetchErr != nil {
			return h.fetchErr
		}
		res, _ := json.Marshal(map[string]any{"status": h.fetchStatus, "body": h.fetchBody})
		return json.Unmarshal(res, out)
	case "log":
		return nil
	}
	h.t.Fatalf("unexpected host call %q", fn)
	return nil
}

func (h *host) dbExec(a map[string]any, out any) error {
	sql := a["sql"].(string)
	args, _ := a["args"].([]any)
	n := 0
	switch {
	case strings.HasPrefix(sql, "INSERT"):
		id := args[0].(string)
		if h.rows[id] == nil {
			h.rows[id] = &row{form: args[1].(string), status: args[2].(string), reason: args[3].(string), test: args[4].(bool)}
			n = 1
		}
	case strings.HasPrefix(sql, "UPDATE"):
		r := h.rows[args[0].(string)]
		r.status, r.reason, r.code, r.message = args[1].(string), args[2].(string), args[3], args[4]
		n = 1
	case strings.HasPrefix(sql, "DELETE"):
	default:
		h.t.Fatalf("unexpected SQL %q", sql)
	}
	res, _ := json.Marshal(map[string]int{"rows_affected": n})
	return json.Unmarshal(res, out)
}

func sample(id string) sdk.FormSubmittedEvent {
	return sdk.FormSubmittedEvent{
		FormID: "f1", FormSlug: "contact", SubmissionID: id, SourcePath: "/contact",
		SubmittedAt: time.Unix(1700000000, 0).UTC(),
		Fields:      map[string]any{"email": sampleEmail, "phone": samplePhone, "name": sampleName, "consent": "Yes"},
	}
}

// sent decodes the URL and body of the one recorded http.fetch call.
func (h *host) sent(t *testing.T) (url string, body map[string]any) {
	t.Helper()
	f := h.callsOf("http.fetch")
	if len(f) != 1 {
		t.Fatalf("fetch calls = %d, want 1", len(f))
	}
	args := f[0].raw.(map[string]any)
	if err := json.Unmarshal([]byte(args["body"].(string)), &body); err != nil {
		t.Fatal(err)
	}
	return args["url"].(string), body
}

func TestDeliverSendsOneEvent(t *testing.T) {
	h := newHost(t)
	h.configured(`"site_url":"https://example.com/"`)
	if err := onFormSubmitted(sample("sub-1")); err != nil {
		t.Fatal(err)
	}
	url, body := h.sent(t)
	if url != "https://graph.facebook.com/v26.0/123456789/events" || strings.Contains(url, sampleToken) {
		t.Fatalf("url = %q", url)
	}
	if body["access_token"] != sampleToken {
		t.Fatalf("token not in the body: %v", body)
	}
	if _, ok := body["test_event_code"]; ok {
		t.Fatal("test_event_code sent without being set")
	}
	d := body["data"].([]any)[0].(map[string]any)
	if d["event_id"] != "sub-1" || d["action_source"] != "website" || d["event_name"] != "Lead" ||
		d["event_time"] != float64(1700000000) || d["event_source_url"] != "https://example.com/contact" {
		t.Fatalf("event = %v", d)
	}
	ud := d["user_data"].(map[string]any)
	if ud["em"].([]any)[0] != sha("john_smith@example.com") || ud["ph"].([]any)[0] != sha("610412345678") ||
		ud["fn"].([]any)[0] != sha("mary") || ud["ln"].([]any)[0] != sha("smith") {
		t.Fatalf("user_data = %v", ud)
	}
	if d["custom_data"].(map[string]any)["form"] != "contact" {
		t.Fatalf("custom_data = %v", d["custom_data"])
	}
	args := h.callsOf("http.fetch")[0].raw.(map[string]any)
	if args["method"] != "POST" || args["timeout_ms"] != 8000 {
		t.Fatalf("fetch args = %v", args)
	}
	r := h.rows["sub-1"]
	if r.status != "sent" || r.reason != "" || r.code != 200 || r.message != nil || r.test {
		t.Fatalf("row = %+v", r)
	}
	assertNoLeaks(t, h)
}

func TestDeliverTwiceSendsOnce(t *testing.T) {
	h := newHost(t)
	h.configured()
	onFormSubmitted(sample("sub-1"))
	onFormSubmitted(sample("sub-1"))
	if h.count("http.fetch") != 1 {
		t.Fatalf("fetch calls = %d, want 1", h.count("http.fetch"))
	}
}

func TestDeliverSkips(t *testing.T) {
	cases := []struct {
		name, reason string
		settings     []string
		mutate       func(*sdk.FormSubmittedEvent)
		unconfigured bool
	}{
		{name: "not configured", reason: "not_configured", unconfigured: true},
		{name: "form filtered", reason: "form_filtered", settings: []string{`"forms":["quote"]`}},
		{name: "consent missing", reason: "consent", settings: []string{`"consent_field":"agree"`}},
		{name: "consent no", reason: "consent", settings: []string{`"consent_field":"agree"`},
			mutate: func(e *sdk.FormSubmittedEvent) { e.Fields["agree"] = "no" }},
		{name: "no contact", reason: "no_contact",
			mutate: func(e *sdk.FormSubmittedEvent) { e.Fields = map[string]any{"name": sampleName} }},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			if !c.unconfigured {
				h.configured(c.settings...)
			}
			ev := sample("sub-1")
			if c.mutate != nil {
				c.mutate(&ev)
			}
			if err := onFormSubmitted(ev); err != nil {
				t.Fatal(err)
			}
			if h.count("http.fetch") != 0 {
				t.Fatal("a skipped delivery must not fetch")
			}
			r := h.rows["sub-1"]
			if len(h.rows) != 1 || r.status != "skipped" || r.reason != c.reason {
				t.Fatalf("rows = %+v", h.rows)
			}
			if h.count("log") != 0 {
				t.Fatal("a skip is not an error and writes no log line")
			}
			assertNoLeaks(t, h)
		})
	}
}

func TestDeliverConsentGiven(t *testing.T) {
	for _, v := range []any{true, 1.0, "yes", " Checked ", "ON", "Y", "1", "true"} {
		h := newHost(t)
		h.configured(`"consent_field":"agree"`)
		ev := sample("sub-1")
		ev.Fields["agree"] = v
		onFormSubmitted(ev)
		if h.count("http.fetch") != 1 {
			t.Errorf("consent %v: fetch calls = %d", v, h.count("http.fetch"))
		}
	}
	for _, v := range []any{false, 0.0, "", "no", "off", []any{"yes"}, nil} {
		if truthy(v) {
			t.Errorf("truthy(%v) = true", v)
		}
	}
}

func TestDeliverFailures(t *testing.T) {
	cases := []struct {
		name   string
		status int
		body   string
		err    error
		code   any
		msg    any
	}{
		{name: "400", status: 400, body: `{"error":{"message":"(#100) Invalid parameter","code":100}}`, code: 100, msg: "(#100) Invalid parameter"},
		{name: "500 no json", status: 500, body: `oops`, code: 500},
		{name: "fetch error", err: errors.New("blocked")},
		{name: "rate limited", err: errors.New("rate_limited")},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			h := newHost(t)
			h.configured()
			h.fetchStatus, h.fetchBody, h.fetchErr = c.status, c.body, c.err
			if err := onFormSubmitted(sample("sub-1")); err != nil {
				t.Fatalf("hook returned %v", err)
			}
			if h.count("http.fetch") != 1 {
				t.Fatalf("fetch calls = %d, want 1 (no retry)", h.count("http.fetch"))
			}
			r := h.rows["sub-1"]
			if r.status != "failed" || r.reason != "http_error" || r.code != c.code || r.message != c.msg {
				t.Fatalf("row = %+v", r)
			}
			if h.count("log") != 1 {
				t.Fatalf("log lines = %d, want 1", h.count("log"))
			}
			assertNoLeaks(t, h)
		})
	}
}

func TestDeliverLongMetaMessageIsCut(t *testing.T) {
	h := newHost(t)
	h.configured()
	h.fetchStatus = 400
	h.fetchBody = `{"error":{"message":"` + strings.Repeat("é", 500) + `","code":1}}`
	onFormSubmitted(sample("sub-1"))
	if got := h.rows["sub-1"].message.(string); len([]rune(got)) != 300 {
		t.Fatalf("message length = %d runes", len([]rune(got)))
	}
}

func TestDeliverInterimRowWhenFetchTraps(t *testing.T) {
	h := newHost(t)
	h.configured()
	h.panicOn = "http.fetch"
	if err := onFormSubmitted(sample("sub-1")); err != nil {
		t.Fatalf("hook returned %v", err)
	}
	r := h.rows["sub-1"]
	if r.status != "failed" || r.reason != "interrupted" {
		t.Fatalf("row = %+v", r)
	}
}

func TestDeliverDBDownSendsNothing(t *testing.T) {
	h := newHost(t)
	h.configured()
	callHost = func(fn string, args, out any) error {
		if fn == "settings.get" {
			return json.Unmarshal([]byte(h.settings), out)
		}
		h.calls = append(h.calls, call{fn: fn})
		if fn == "db.exec" {
			return errHostDown
		}
		return nil
	}
	if err := onFormSubmitted(sample("sub-1")); err != nil {
		t.Fatal(err)
	}
	if h.count("http.fetch") != 0 {
		t.Fatal("no dedupe row, so no send")
	}
}

func TestDeliverTestEventCodeOnlyWhenSet(t *testing.T) {
	h := newHost(t)
	h.configured(`"test_event_code":"TEST123"`)
	onFormSubmitted(sample("sub-1"))
	_, body := h.sent(t)
	if body["test_event_code"] != "TEST123" {
		t.Fatalf("body = %v", body)
	}
}

func TestDeliverOmitsSourceURLWithoutSiteURL(t *testing.T) {
	h := newHost(t)
	h.configured()
	onFormSubmitted(sample("sub-1"))
	_, body := h.sent(t)
	if _, ok := body["data"].([]any)[0].(map[string]any)["event_source_url"]; ok {
		t.Fatal("event_source_url must be omitted when site_url is empty")
	}
}

func TestDeliverPrunesAfterInsert(t *testing.T) {
	h := newHost(t)
	h.configured()
	onFormSubmitted(sample("sub-1"))
	var kinds []string
	for _, c := range h.callsOf("db.exec") {
		sql := c.raw.(map[string]any)["sql"].(string)
		kinds = append(kinds, strings.Fields(sql)[0])
		if strings.HasPrefix(sql, "DELETE") && !strings.Contains(sql, "interval '30 days'") {
			t.Fatalf("prune sql = %q", sql)
		}
	}
	if !slices.Equal(kinds, []string{"INSERT", "DELETE", "UPDATE"}) {
		t.Fatalf("statements = %v", kinds)
	}
	// A duplicate inserts nothing and prunes nothing.
	h.calls = nil
	onFormSubmitted(sample("sub-1"))
	if len(h.callsOf("db.exec")) != 1 {
		t.Fatalf("duplicate ran %d statements, want only the insert attempt", len(h.callsOf("db.exec")))
	}
}

func TestDeliverUsesMappedFields(t *testing.T) {
	h := newHost(t)
	h.configured(`"email_field":"mail","phone_field":"tel","name_field":"who"`)
	ev := sample("sub-1")
	ev.Fields = map[string]any{"mail": "a@example.com", "tel": float64(61412345678), "who": "Zoë"}
	onFormSubmitted(ev)
	_, body := h.sent(t)
	ud := body["data"].([]any)[0].(map[string]any)["user_data"].(map[string]any)
	if ud["em"].([]any)[0] != sha("a@example.com") || ud["ph"].([]any)[0] != sha("61412345678") ||
		ud["fn"].([]any)[0] != sha("zoë") {
		t.Fatalf("user_data = %v", ud)
	}
	if _, ok := ud["ln"]; ok {
		t.Fatal("single word name has no last name")
	}
}

// assertNoLeaks scans every recorded host call. Hashes and the token may only
// appear in the body of the fetch; field values appear nowhere.
func assertNoLeaks(t *testing.T, h *host) {
	t.Helper()
	plain := []string{sampleEmail, strings.ToLower(sampleEmail), samplePhone, "610412345678", "4123456", sampleName, "Mary", "Smith"}
	hashes := []string{sha("john_smith@example.com"), sha("610412345678"), sha("mary"), sha("smith")}
	for _, c := range h.calls {
		hay := c.args
		if c.fn == "http.fetch" {
			args := c.raw.(map[string]any)
			for _, s := range hashes {
				if strings.Contains(args["url"].(string), s) {
					t.Errorf("hash in the fetch url")
				}
			}
			if strings.Contains(args["url"].(string), sampleToken) {
				t.Errorf("token in the fetch url")
			}
			hay = args["url"].(string)
			body := args["body"].(string)
			for _, s := range plain {
				if strings.Contains(body, s) {
					t.Errorf("plain value %q in the fetch body", s)
				}
			}
		} else {
			for _, s := range append(slices.Clone(hashes), sampleToken) {
				if strings.Contains(hay, s) {
					t.Errorf("%s call leaks a hash or the token: %s", c.fn, hay)
				}
			}
		}
		for _, s := range plain {
			if strings.Contains(hay, s) {
				t.Errorf("%s call leaks %q: %s", c.fn, s, hay)
			}
		}
	}
}

func TestEventSourceURLFromSourcePath(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"path", "/contact", "https://example.com/contact"},
		{"path with query", "/contact?x=1#top", "https://example.com/contact"},
		{"full url", "https://other.example.org/contact/us?fbclid=abc&x=1#frag", "https://example.com/contact/us"},
		{"full url no path", "https://other.example.org?fbclid=abc", ""},
		{"no leading slash", "contact", ""},
		{"protocol relative", "//evil.example.org/x", ""},
		{"whitespace", "/a b", ""},
		{"control char", "/a\nb", ""},
		{"garbage", "javascript:alert(1)", ""},
		{"empty", "", ""},
		{"overlong", "/" + strings.Repeat("a", 600), ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			s := settings{SiteURL: "https://example.com", EventName: "Lead"}
			ev := sample("sub-1")
			ev.SourcePath = c.in
			var body struct {
				Data []map[string]any `json:"data"`
			}
			if err := json.Unmarshal(buildEvent(s, ev, userData{}, ""), &body); err != nil {
				t.Fatal(err)
			}
			want := c.want
			if want == "" {
				want = "https://example.com"
			}
			if got := body.Data[0]["event_source_url"]; got != want {
				t.Fatalf("event_source_url = %v, want %q", got, want)
			}
		})
	}
}

func TestDeliverInvalidPixelOrVersionIsNotConfigured(t *testing.T) {
	for name, extra := range map[string]string{
		"pixel":   `"pixel_id":"123/../x"`,
		"version": `"graph_version":"v26.0/../x"`,
	} {
		t.Run(name, func(t *testing.T) {
			h := newHost(t)
			h.settings = `{"pixel_id":"123456789","access_token":"` + sampleToken + `",` + extra + `}`
			if err := onFormSubmitted(sample("sub-1")); err != nil {
				t.Fatal(err)
			}
			if h.count("http.fetch") != 0 {
				t.Fatal("fetch made with an invalid setting")
			}
			if r := h.rows["sub-1"]; r == nil || r.status != "skipped" || r.reason != "not_configured" {
				t.Fatalf("row = %+v", r)
			}
		})
	}
}
