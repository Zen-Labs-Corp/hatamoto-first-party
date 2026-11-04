package main

import (
	"encoding/json"
	"errors"
	"testing"
	"time"
)

// jsonHost answers every host call by decoding body into the result.
func jsonHost(body string) func(string, any, any) error {
	return func(_ string, _, out any) error { return json.Unmarshal([]byte(body), out) }
}

func TestParseTimeVariants(t *testing.T) {
	want := time.Date(2026, 11, 4, 10, 30, 0, 0, time.UTC)
	for in, w := range map[string]time.Time{
		"2026-11-04T10:30:00Z":      want,
		"2026-11-04T10:30":          want,
		"2026-11-04T10:30:00":       want,
		" 2026-11-04T10:30 ":        want,
		"2026-11-04":                time.Date(2026, 11, 4, 0, 0, 0, 0, time.UTC),
		"2026-11-04T11:30:00+01:00": want,
	} {
		got, ok := parseTime(in)
		if !ok || !got.Equal(w) {
			t.Errorf("parseTime(%q) = %v %v", in, got, ok)
		}
	}
	for _, in := range []string{"", "soon", "04/11/2026"} {
		if _, ok := parseTime(in); ok {
			t.Errorf("parseTime(%q) parsed", in)
		}
	}
}

func TestOpenAndPublicTruthTable(t *testing.T) {
	end := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	before, after := end.Add(-time.Second), end
	cases := []struct {
		name                                       string
		cfg                                        formCfg
		openBefore, openAfter, pubBefore, pubAfter bool
	}{
		{"always, no close", formCfg{ShowResults: showAlways}, true, true, true, true},
		{"never, closes", formCfg{ShowResults: showNever, ClosesAt: "2026-11-04T12:00"}, true, false, false, false},
		{"after_close, closes", formCfg{ShowResults: showAfterClose, ClosesAt: "2026-11-04T12:00"}, true, false, false, true},
		{"after_close, no close", formCfg{ShowResults: showAfterClose}, true, true, false, false},
		{"always, unreadable close", formCfg{ShowResults: showAlways, ClosesAt: "soon"}, true, true, true, true},
	}
	for _, c := range cases {
		got := [4]bool{c.cfg.open(before), c.cfg.open(after), c.cfg.public(before), c.cfg.public(after)}
		want := [4]bool{c.openBefore, c.openAfter, c.pubBefore, c.pubAfter}
		if got != want {
			t.Errorf("%s: got %v want %v", c.name, got, want)
		}
	}
}

func TestLoadConfigNormalises(t *testing.T) {
	callHost = jsonHost(`{"forms":[
		{"form_slug":" a ","kind":"quiz","show_results":"always","answer_key":[{"field":"q1","value":"x"}],"email_score":true},
		{"form_slug":"a","kind":"poll"},
		{"form_slug":"  "},
		{"form_slug":"b","kind":"weird","show_results":"sometimes"}]}`)
	got, err := loadConfig()
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 {
		t.Fatalf("got %+v", got)
	}
	if got[0].Slug != "a" || got[0].Kind != kindQuiz || got[0].ShowResults != showAlways || !got[0].EmailScore || len(got[0].Key) != 1 {
		t.Errorf("first = %+v", got[0])
	}
	if got[1].Slug != "b" || got[1].Kind != kindPoll || got[1].ShowResults != showNever {
		t.Errorf("second = %+v", got[1])
	}
}

func TestLoadConfigEmptyAndHostError(t *testing.T) {
	callHost = jsonHost(`{}`)
	if got, err := loadConfig(); err != nil || len(got) != 0 {
		t.Fatalf("empty: %v %v", got, err)
	}
	callHost = func(string, any, any) error { return errors.New("down") }
	if _, err := loadConfig(); err == nil {
		t.Fatal("host error must be returned")
	}
}

func TestBlankAnswerKeyRowsAreIgnored(t *testing.T) {
	f := &fake{t: t, settings: `{"forms":[{"form_slug":"fw","kind":"quiz","answer_key":[{"field":"fw","value":"go"},{},{"field":"  ","value":"x"}]}]}`}
	f.install()
	cfgs, err := loadConfig()
	if err != nil || len(cfgs) != 1 || len(cfgs[0].Key) != 1 {
		t.Fatalf("cfgs %+v err %v", cfgs, err)
	}
	_, max := score(cfgs[0], nil, nil)
	if max != 1 {
		t.Fatalf("max %d, want 1", max)
	}
}
