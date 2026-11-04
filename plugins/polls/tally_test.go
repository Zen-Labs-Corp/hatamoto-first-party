package main

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

type hostCall struct {
	fn   string
	sql  string
	args []any
	raw  any // the arguments as the plugin passed them
}

// fake is a scriptable host: canned settings, forms and errors, and a record of
// every call.
type fake struct {
	t        *testing.T
	settings string
	forms    string
	errs     map[string]error // by host function
	calls    []hostCall
	answer   func(c hostCall, out any) error // optional: other host functions, and results for db.exec

}

func (f *fake) install() {
	callHost = func(fn string, a, out any) error {
		c := hostCall{fn: fn, raw: a}
		if m, ok := a.(map[string]any); ok {
			c.sql, _ = m["sql"].(string)
			c.args, _ = m["args"].([]any)
		}
		if fn == "mail.send" || fn == "log" {
			c.args = []any{a}
		}
		f.calls = append(f.calls, c)
		if err := f.errs[fn]; err != nil {
			return err
		}
		switch fn {
		case "settings.get":
			return json.Unmarshal([]byte(f.settings), out)
		case "forms.list":
			return json.Unmarshal([]byte(f.forms), out)
		case "log", "mail.send", "db.exec":
			if f.answer != nil {
				return f.answer(c, out)
			}
			return nil
		}
		if f.answer != nil {
			return f.answer(c, out)
		}
		f.t.Fatalf("unexpected host call %q", fn)
		return nil
	}
}

func (f *fake) named(fn string) []hostCall {
	var out []hostCall
	for _, c := range f.calls {
		if c.fn == fn {
			out = append(out, c)
		}
	}
	return out
}

const formsJSON = `{"items":[{"slug":"fw","name":"Framework","fields":[
 {"name":"fw","label":"Which framework?","type":"radio","options":[{"label":"Go","value":"go"},{"label":"Rust","value":"rust"}]},
 {"name":"tools","label":"Tools","type":"checkbox","options":[{"label":"Vim","value":"vim"},{"label":"Emacs","value":"emacs"},{"label":"Nano","value":"nano"}]},
 {"name":"mail","label":"Email","type":"email"},
 {"name":"note","label":"Note","type":"textarea"},
 {"name":"agree","label":"Agree","type":"checkbox"}]}]}`

func runSubmit(t *testing.T, f *fake, e sdk.FormSubmittedEvent) {
	t.Helper()
	f.t = t
	f.install()
	in, _ := json.Marshal(e)
	if _, err := guest.Handle(sdk.FormSubmitted.Export(), in); err != nil {
		t.Fatalf("hook returned an error: %v", err)
	}
}

func TestCountForChoices(t *testing.T) {
	var forms struct{ Items []formDef }
	_ = json.Unmarshal([]byte(formsJSON), &forms)
	fields := choiceFields(forms.Items[0].Fields)
	if len(fields) != 2 {
		t.Fatalf("choice fields: %+v", fields)
	}
	got := countFor(fields, map[string]any{
		"fw":    "go",
		"tools": []any{"vim", "vim", "nano", "ed"},
		"note":  "hello",
		"agree": true,
	})
	want := map[[2]string]int{{"fw", "go"}: 1, {"tools", "vim"}: 1, {"tools", "nano"}: 1, {"tools", otherOption}: 1}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %v want %v", got, want)
	}
	if n := len(countFor(fields, map[string]any{"fw": "java"})); n != 1 {
		t.Fatalf("unknown value: %d pairs", n)
	}
	if n := len(countFor(fields, map[string]any{"fw": 3.0, "tools": true})); n != 0 {
		t.Fatalf("non-string answers counted: %d", n)
	}
}

func TestScore(t *testing.T) {
	var forms struct{ Items []formDef }
	_ = json.Unmarshal([]byte(formsJSON), &forms)
	fields := forms.Items[0].Fields
	cfg := formCfg{Key: []keyPair{{"fw", "Go"}, {"tools", "vim, emacs"}, {"ghost", "x"}}}
	cases := []struct {
		name string
		data map[string]any
		got  int
	}{
		{"all known right", map[string]any{"fw": " GO ", "tools": []any{"emacs", "vim"}}, 2},
		{"checkbox subset is wrong", map[string]any{"fw": "go", "tools": []any{"vim"}}, 1},
		{"checkbox superset is wrong", map[string]any{"fw": "rust", "tools": []any{"vim", "emacs", "nano"}}, 0},
		{"unanswered", map[string]any{}, 0},
	}
	for _, c := range cases {
		got, max := score(cfg, fields, c.data)
		if got != c.got || max != 3 {
			t.Errorf("%s: %d/%d", c.name, got, max)
		}
	}
}

func TestScoreNumberAndTickbox(t *testing.T) {
	fields := []fieldDef{{Name: "planets", Type: "number"}, {Name: "agree", Type: "checkbox"}, {Name: "ratio", Type: "number"}}
	cfg := formCfg{Key: []keyPair{{"planets", "8"}, {"agree", "true"}, {"ratio", "2.5"}}}
	got, max := score(cfg, fields, map[string]any{"planets": 8.0, "agree": true, "ratio": 2.5})
	if got != 3 || max != 3 {
		t.Fatalf("score %d/%d, want 3/3", got, max)
	}
	if got, _ := score(cfg, fields, map[string]any{"planets": 9.0, "agree": false, "ratio": 2.0}); got != 0 {
		t.Fatalf("wrong answers scored %d", got)
	}
}

func TestSubmissionIsOneUpsertStatement(t *testing.T) {
	f := &fake{settings: `{"forms":[{"form_slug":"fw","kind":"poll"}]}`, forms: formsJSON}
	runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "s1",
		Fields: map[string]any{"fw": "go", "tools": []any{"vim", "zed"}, "note": "x"}})
	execs := f.named("db.exec")
	if len(execs) != 1 || execs[0].sql != upsertSubmission {
		t.Fatalf("db calls: %+v", f.calls)
	}
	a := execs[0].args
	if a[0] != "fw" || a[1] != "s1" || a[2] != nil || a[3] != nil {
		t.Fatalf("args %v", a)
	}
	want := []tallyRow{{"fw", "go"}, {"tools", otherOption}, {"tools", "vim"}}
	if !reflect.DeepEqual(a[4], want) {
		t.Fatalf("tallies %v", a[4])
	}
	if len(f.named("mail.send")) != 0 {
		t.Fatal("a poll sends no mail")
	}
}

func TestSubmissionNotCounted(t *testing.T) {
	end := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	for name, c := range map[string]struct {
		settings string
		slug     string
		at       time.Time
		errs     map[string]error
	}{
		"after closes_at":  {`{"forms":[{"form_slug":"fw","closes_at":"2026-11-04T12:00"}]}`, "fw", end, nil},
		"unknown form":     {`{"forms":[{"form_slug":"fw"}]}`, "other", end, nil},
		"settings failure": {`{}`, "fw", end, map[string]error{"settings.get": errors.New("down")}},
		"forms failure":    {`{"forms":[{"form_slug":"fw"}]}`, "fw", end, map[string]error{"forms.list": errors.New("down")}},
	} {
		f := &fake{settings: c.settings, forms: formsJSON, errs: c.errs}
		runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: c.slug, SubmissionID: "s", SubmittedAt: c.at, Fields: map[string]any{"fw": "go"}})
		if n := len(f.named("db.exec")); n != 0 {
			t.Errorf("%s: %d db calls", name, n)
		}
	}
	// Just before closing still counts.
	f := &fake{settings: `{"forms":[{"form_slug":"fw","closes_at":"2026-11-04T12:00"}]}`, forms: formsJSON}
	runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "s", SubmittedAt: end.Add(-time.Second), Fields: map[string]any{"fw": "go"}})
	if len(f.named("db.exec")) != 1 {
		t.Fatal("submission before closes_at not counted")
	}
}

func TestQuizStoresScoreAndMailsOnce(t *testing.T) {
	f := &fake{
		settings: `{"forms":[{"form_slug":"fw","kind":"quiz","email_score":true,"answer_key":[{"field":"fw","value":"go"},{"field":"tools","value":"vim,emacs"}]}]}`,
		forms:    formsJSON,
	}
	runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "q1",
		Fields: map[string]any{"fw": "go", "tools": []any{"vim"}, "mail": "a@example.org"}})
	a := f.named("db.exec")[0].args
	if a[2] != 1 || a[3] != 2 {
		t.Fatalf("score args %v", a)
	}
	mails := f.named("mail.send")
	if len(mails) != 1 {
		t.Fatalf("%d mail.send calls", len(mails))
	}
	m := mails[0].args[0].(map[string]any)
	if got := m["to"].([]string); len(got) != 1 || got[0] != "a@example.org" || m["text"] != "You scored 1 of 2." {
		t.Fatalf("mail %v", m)
	}
}

func TestQuizMailRateLimitedFailsOpen(t *testing.T) {
	f := &fake{
		settings: `{"forms":[{"form_slug":"fw","kind":"quiz","email_score":true,"answer_key":[{"field":"fw","value":"go"}]}]}`,
		forms:    formsJSON,
		errs:     map[string]error{"mail.send": &sdk.HostError{Code: sdk.ErrRateLimited, Message: "slow down"}},
	}
	runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "q1",
		Fields: map[string]any{"fw": "go", "mail": "a@example.org"}})
	if n := len(f.named("mail.send")); n != 1 {
		t.Fatalf("%d mail.send calls, want 1 and no retry", n)
	}
	if len(f.named("db.exec")) != 1 {
		t.Fatal("tally must stay")
	}
	for _, l := range f.named("log") {
		if s := l.args[0].(map[string]string)["message"]; strings.Contains(s, "example.org") {
			t.Fatalf("log leaks the address: %q", s)
		}
	}
}

func TestQuizWithoutEmailOrSwitchSendsNothing(t *testing.T) {
	key := `"answer_key":[{"field":"fw","value":"go"}]`
	for name, c := range map[string]struct {
		settings string
		data     map[string]any
	}{
		"no address": {`{"forms":[{"form_slug":"fw","kind":"quiz","email_score":true,` + key + `}]}`, map[string]any{"fw": "go"}},
		"switch off": {`{"forms":[{"form_slug":"fw","kind":"quiz",` + key + `}]}`, map[string]any{"fw": "go", "mail": "a@example.org"}},
		"survey, on": {`{"forms":[{"form_slug":"fw","kind":"survey","email_score":true,` + key + `}]}`, map[string]any{"fw": "go", "mail": "a@example.org"}},
	} {
		f := &fake{settings: c.settings, forms: formsJSON}
		runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "q", Fields: c.data})
		if n := len(f.named("mail.send")); n != 0 {
			t.Errorf("%s: %d mail.send calls", name, n)
		}
	}
}

func TestEveryStatementIsTheUpsert(t *testing.T) {
	f := &fake{settings: `{"forms":[{"form_slug":"fw"}]}`, forms: formsJSON}
	runSubmit(t, f, sdk.FormSubmittedEvent{FormSlug: "fw", SubmissionID: "s", Fields: map[string]any{"fw": "go"}})
	for _, c := range f.calls {
		if c.sql != "" && c.sql != upsertSubmission {
			t.Errorf("unexpected statement %q", c.sql)
		}
		if c.fn == "db.query" {
			t.Error("the live path must not read before it writes")
		}
	}
}
