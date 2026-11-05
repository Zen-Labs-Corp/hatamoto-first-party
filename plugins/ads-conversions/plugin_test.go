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

const (
	sampleEmail = "Jane.Doe@Example.com"
	samplePhone = "+49 151 234 5678"
	sampleName  = "Jane Doe"
	sampleGclid = "Cj0KCQ-sample-gclid"
)

var testNow = time.Date(2026, 11, 5, 10, 0, 0, 0, time.UTC)

type call struct {
	fn   string
	args string // JSON of the arguments, for the leak scan
	raw  any
}

// fakeHost records every host call and answers the few the plugin makes.
type fakeHost struct {
	t        *testing.T
	settings string
	kv       map[string]any
	calls    []call
	// affected is what db.exec reports as rows_affected.
	affected int64
	// failExec makes db.exec fail; panicOn makes the named call panic.
	failExec bool
	panicOn  string
	// mailErr is returned by mail.send.
	mailErr error
	// query maps a SQL fragment to the jsonb cell db.query returns.
	query map[string]string
}

func newHost(t *testing.T) *fakeHost {
	h := &fakeHost{t: t, kv: map[string]any{}, affected: 1, query: map[string]string{}}
	callHost = h.call
	now = func() time.Time { return testNow }
	t.Cleanup(func() { now = time.Now })
	return h
}

// configure turns on the three platforms with complete settings; extra
// key:value pairs are merged in (later keys win).
func (h *fakeHost) configure(extra ...string) {
	s := `{"public_url":"https://example.com","forms":["contact"],
	"google_enabled":true,"google_client_id":"gid","google_client_secret":"gsecret","google_customer_id":"123-456-7890","google_conversion_action_id":"987",
	"tiktok_enabled":true,"tiktok_pixel_code":"CABC123","tiktok_access_token":"tok-secret",
	"linkedin_enabled":true,"linkedin_client_id":"lid","linkedin_client_secret":"lsecret","linkedin_conversion_rule_id":"5"`
	for _, e := range extra {
		s += "," + e
	}
	h.settings = s + "}"
}

func (h *fakeHost) callsOf(fn string) []call {
	var out []call
	for _, c := range h.calls {
		if c.fn == fn {
			out = append(out, c)
		}
	}
	return out
}

// execs returns the db.exec calls whose SQL starts with prefix.
func (h *fakeHost) execs(prefix string) []call {
	var out []call
	for _, c := range h.callsOf("db.exec") {
		if strings.HasPrefix(c.raw.(map[string]any)["sql"].(string), prefix) {
			out = append(out, c)
		}
	}
	return out
}

func (h *fakeHost) call(fn string, args, out any) error {
	b, _ := json.Marshal(args)
	h.calls = append(h.calls, call{fn, string(b), args})
	if fn == h.panicOn {
		panic("boom")
	}
	switch fn {
	case "settings.get":
		raw := h.settings
		if raw == "" {
			raw = "{}"
		}
		return json.Unmarshal([]byte(raw), out)
	case "db.exec":
		if h.failExec {
			return errors.New("db down")
		}
		return json.Unmarshal([]byte(`{"rows_affected":`+string(rune('0'+h.affected))+`}`), out)
	case "db.query":
		sql := args.(map[string]any)["sql"].(string)
		for frag, cell := range h.query {
			if strings.Contains(sql, frag) {
				return json.Unmarshal([]byte(`{"rows":[[`+cell+`]]}`), out)
			}
		}
		return json.Unmarshal([]byte(`{"rows":[[[]]]}`), out)
	case "kv.get":
		key := args.(map[string]string)["key"]
		v, ok := h.kv[key]
		res, _ := json.Marshal(map[string]any{"found": ok, "value": v})
		return json.Unmarshal(res, out)
	case "kv.set":
		a := args.(map[string]any)
		h.kv[a["key"].(string)] = a["value"]
		return nil
	case "kv.delete":
		delete(h.kv, args.(map[string]string)["key"])
		return nil
	case "mail.send":
		return h.mailErr
	case "log":
		return nil
	}
	h.t.Fatalf("unexpected host call %q", fn)
	return nil
}

func event(slug string, fields map[string]any) sdk.FormSubmittedEvent {
	return sdk.FormSubmittedEvent{FormSlug: slug, SubmissionID: "7f3c1d52-0000-4000-8000-00000000a91e", Fields: fields,
		SubmittedAt: testNow.Add(-time.Second)}
}

func contact() map[string]any {
	return map[string]any{"email": sampleEmail, "phone": samplePhone, "name": sampleName, "consent": "yes"}
}

// run delivers the event through the registered hook, as the host does.
func run(t *testing.T, ev sdk.FormSubmittedEvent) error {
	t.Helper()
	in, _ := json.Marshal(ev)
	_, err := guest.Handle(sdk.FormSubmitted.Export(), in)
	return err
}

func queuedPlatforms(h *fakeHost) []string {
	var out []string
	for _, c := range h.execs("INSERT INTO queue") {
		out = append(out, c.raw.(map[string]any)["args"].([]any)[2].(string))
	}
	return out
}

func TestHookQueuesOneRowPerActivePlatform(t *testing.T) {
	h := newHost(t)
	h.configure(`"linkedin_enabled":false`)
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(queuedPlatforms(h), ","); got != "google,tiktok" {
		t.Fatalf("queued = %q", got)
	}
	if n := len(h.callsOf("http.fetch")) + len(h.callsOf("mail.send")); n != 0 {
		t.Fatalf("the hook made %d platform calls", n)
	}
	sql := h.execs("INSERT INTO queue")[0].raw.(map[string]any)["sql"].(string)
	if !strings.Contains(sql, "ON CONFLICT (submission_id, platform) DO NOTHING") {
		t.Fatal("enqueue must be idempotent per submission and platform")
	}
}

func TestHookQueuesHashesAndPathNotRawValues(t *testing.T) {
	h := newHost(t)
	h.configure(`"consent_field":"consent"`)
	ev := event("contact", contact())
	ev.SourcePath = "/thanks?email=jane%40example.com&gclid=" + sampleGclid
	if err := run(t, ev); err != nil {
		t.Fatal(err)
	}
	args := h.execs("INSERT INTO queue")[0].raw.(map[string]any)["args"].([]any)
	if args[3] != sha("jane.doe@example.com") || args[4] != sha("janedoe@example.com") && args[4] != sha("jane.doe@example.com") ||
		args[5] != sha("+491512345678") || args[6] != sampleGclid || args[11] != "/thanks" || args[12] != true {
		t.Fatalf("args = %v", args)
	}
	for _, c := range h.calls {
		for _, bad := range []string{sampleEmail, strings.ToLower(sampleEmail), "491512345678", sampleName, "jane%40example", "email="} {
			if strings.Contains(c.args, bad) {
				t.Fatalf("%s call carries %q: %s", c.fn, bad, c.args)
			}
		}
	}
}

func TestHookIgnoresFormsNotListedAndEmptyList(t *testing.T) {
	h := newHost(t)
	h.configure()
	if err := run(t, event("quote", contact())); err != nil || len(h.execs("")) != 0 {
		t.Fatalf("unlisted form: err %v, execs %d", err, len(h.execs("")))
	}
	h.configure(`"forms":[]`)
	if err := run(t, event("contact", contact())); err != nil || len(h.execs("")) != 0 {
		t.Fatalf("empty forms list must send nothing: err %v", err)
	}
}

func TestHookSkipsUnconfiguredDisabledAndBrokenPlatforms(t *testing.T) {
	h := newHost(t)
	h.configure(`"tiktok_access_token":""`, `"linkedin_enabled":false`)
	h.kv["status:google"] = "reconnect"
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatal(err)
	}
	if len(h.execs("")) != 0 {
		t.Fatalf("nothing should be queued: %v", queuedPlatforms(h))
	}
	h.kv["status:google"] = "ok"
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(queuedPlatforms(h), ","); got != "google" {
		t.Fatalf("queued = %q", got)
	}
}

func TestHookConsent(t *testing.T) {
	h := newHost(t)
	h.configure(`"consent_field":"consent"`)
	f := contact()
	f["consent"] = ""
	if err := run(t, event("contact", f)); err != nil {
		t.Fatal(err)
	}
	if len(queuedPlatforms(h)) != 0 {
		t.Fatal("queued without consent")
	}
	logs := h.execs("INSERT INTO log")
	if len(logs) != 1 || !strings.Contains(logs[0].args, `"-"`) || !strings.Contains(logs[0].args, `"consent"`) ||
		!strings.Contains(logs[0].raw.(map[string]any)["sql"].(string), "WHERE NOT EXISTS") {
		t.Fatalf("log rows = %+v", logs)
	}
	f["consent"] = true
	h.calls = nil
	if err := run(t, event("contact", f)); err != nil || len(queuedPlatforms(h)) != 3 {
		t.Fatalf("with consent: err %v, queued %v", err, queuedPlatforms(h))
	}
}

func TestHookNoContact(t *testing.T) {
	h := newHost(t)
	h.configure()
	if err := run(t, event("contact", map[string]any{"email": "nope", "phone": "12", "message": "hi"})); err != nil {
		t.Fatal(err)
	}
	logs := h.execs("INSERT INTO log")
	if len(queuedPlatforms(h)) != 0 || len(logs) != 1 || !strings.Contains(logs[0].args, "no_contact") {
		t.Fatalf("queued %v, logs %+v", queuedPlatforms(h), logs)
	}
	// A click ID in the page address alone is enough to queue.
	h.calls = nil
	ev := event("contact", map[string]any{"message": "hi"})
	ev.SourcePath = "/x?gclid=" + sampleGclid
	if err := run(t, ev); err != nil || len(queuedPlatforms(h)) != 3 {
		t.Fatalf("err %v, queued %v", err, queuedPlatforms(h))
	}
}

func TestHookDuplicateDeliveryQueuesNothingNew(t *testing.T) {
	h := newHost(t)
	h.configure()
	h.affected = 0 // ON CONFLICT DO NOTHING reports no row
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatal(err)
	}
	for _, p := range platforms {
		fresh, err := enqueue(job{SubmissionID: "s", Platform: p})
		if err != nil || fresh {
			t.Fatalf("%s: fresh %v err %v", p, fresh, err)
		}
	}
}

func TestHookNeverErrors(t *testing.T) {
	h := newHost(t)
	h.configure()
	h.failExec = true
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatalf("db failure: %v", err)
	}
	h = newHost(t)
	h.configure()
	h.panicOn = "db.exec"
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatalf("panic: %v", err)
	}
	h = newHost(t)
	h.panicOn = "settings.get"
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatalf("settings panic: %v", err)
	}
	callHost = func(string, any, any) error { return errors.New("host down") }
	if err := run(t, event("contact", contact())); err != nil {
		t.Fatalf("host down: %v", err)
	}
}

func TestHookLeaksNothingToLogsOrLogRows(t *testing.T) {
	h := newHost(t)
	h.configure(`"consent_field":"consent"`)
	h.failExec = true
	f := contact()
	_ = run(t, event("contact", f))
	f["consent"] = ""
	h.failExec = false
	_ = run(t, event("contact", f))
	secrets := []string{sampleEmail, strings.ToLower(sampleEmail), "491512345678", sampleName, sha("jane.doe@example.com"), sha("+491512345678"), "tok-secret", "gsecret", "lsecret"}
	scanned := 0
	for _, c := range h.calls {
		if c.fn != "log" && !(c.fn == "db.exec" && strings.Contains(c.args, "INSERT INTO log")) {
			continue
		}
		scanned++
		for _, s := range secrets {
			if strings.Contains(c.args, s) {
				t.Fatalf("%s call leaks %q: %s", c.fn, s, c.args)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("no log calls were scanned")
	}
}

// broken reads the platform's broken state against the current settings.
func broken(platform string) bool {
	s, _ := loadSettings()
	return isBroken(s, platform)
}
