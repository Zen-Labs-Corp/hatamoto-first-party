package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// serveReq runs a route through the registered HTTP handler.
func serveReq(t *testing.T, method, pattern string, values map[string]string) sdk.HTTPResponse {
	t.Helper()
	in, _ := json.Marshal(sdk.HTTPRequest{Method: method, Pattern: pattern, PathValues: values})
	out, err := guest.Handle(guest.HTTPExport, in)
	if err != nil {
		t.Fatalf("route returned an error: %v", err)
	}
	var res sdk.HTTPResponse
	if err := json.Unmarshal(out, &res); err != nil {
		t.Fatal(err)
	}
	return res
}

// answerResults serves doc for the results query and accepts every db.exec.
func answerResults(doc string) func(hostCall, any) error {
	return func(c hostCall, out any) error {
		switch {
		case c.fn == "db.query" && c.sql == qResults:
			return json.Unmarshal([]byte(`{"rows":[[`+doc+`]]}`), out)
		case c.fn == "db.exec":
			return nil
		}
		return errors.New("unexpected " + c.fn)
	}
}

const resultsDoc = `{"responses":10,
 "tallies":[{"field":"fw","option":"go","n":6},{"field":"fw","option":"rust","n":3},{"field":"fw","option":"zig","n":1},
            {"field":"tools","option":"vim","n":4},{"field":"tools","option":"__other__","n":2}],
 "scores":[{"score":1,"n":4},{"score":2,"n":6}]}`

func TestOverviewBuildsPerQuestionResults(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, answer: answerResults(resultsDoc),
		settings: `{"forms":[{"form_slug":"fw","kind":"quiz","show_results":"always","answer_key":[{"field":"fw","value":"go"},{"field":"tools","value":"vim,emacs"}]}]}`}
	f.install()
	res := serveReq(t, "GET", "/overview", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("status %d %s", res.Status, res.Body)
	}
	var out struct{ Forms []formResult }
	if err := json.Unmarshal([]byte(res.Body), &out); err != nil {
		t.Fatal(err)
	}
	if len(out.Forms) != 1 {
		t.Fatalf("%+v", out)
	}
	fr := out.Forms[0]
	if fr.Name != "Framework" || fr.Responses != 10 || fr.MaxScore != 2 || fr.Average != 1.6 {
		t.Fatalf("form %+v", fr)
	}
	fw := fr.Questions[0]
	// zig is no longer an option: it joins Other. Radio percent is of the answers to that question.
	want := []optionResult{
		{"go", "Go", 6, 60, true}, {"rust", "Rust", 3, 30, false}, {otherOption, "Other", 1, 10, false},
	}
	for i, w := range want {
		if fw.Options[i] != w {
			t.Errorf("fw option %d = %+v want %+v", i, fw.Options[i], w)
		}
	}
	tools := fr.Questions[1] // checkbox percent is of responses; 0-count options are listed
	if len(tools.Options) != 4 || tools.Options[0].Count != 4 || tools.Options[0].Percent != 40 || !tools.Options[0].Correct ||
		tools.Options[1].Count != 0 || !tools.Options[1].Correct || tools.Options[2].Correct || tools.Options[3].Label != "Other" {
		t.Fatalf("tools %+v", tools.Options)
	}
	if len(fr.Scores) != 3 || fr.Scores[0].Count != 0 || fr.Scores[1].Count != 4 || fr.Scores[2].Count != 6 {
		t.Fatalf("scores %+v", fr.Scores)
	}
	if len(f.named("db.query")) != 1 {
		t.Fatal("one results query per form")
	}
}

func TestOverviewEmptySettings(t *testing.T) {
	f := &fake{t: t, settings: `{}`, answer: answerResults(`{}`)}
	f.install()
	res := serveReq(t, "GET", "/overview", nil)
	if res.Status != http.StatusOK || res.Body != `{"forms":[]}` {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if len(f.named("forms.list")) != 0 {
		t.Fatal("no form to look up")
	}
}

func TestOverviewUnknownFormIsNotAnError(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, answer: answerResults(resultsDoc), settings: `{"forms":[{"form_slug":"gone"}]}`}
	f.install()
	res := serveReq(t, "GET", "/overview", nil)
	var out struct{ Forms []formResult }
	_ = json.Unmarshal([]byte(res.Body), &out)
	if res.Status != http.StatusOK || len(out.Forms) != 1 || out.Forms[0].Note != "form not found" {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if len(f.named("db.query")) != 0 {
		t.Fatal("no query for a form core does not know")
	}
}

func TestOverviewIgnoresPurgeFailure(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, settings: `{"forms":[{"form_slug":"fw"}]}`}
	inner := answerResults(resultsDoc)
	f.answer = func(c hostCall, out any) error {
		if c.sql == purgeRemoved {
			return errors.New("db down")
		}
		return inner(c, out)
	}
	f.install()
	if res := serveReq(t, "GET", "/overview", nil); res.Status != http.StatusOK {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if n := len(f.named("db.exec")); n != 1 {
		t.Fatalf("%d purge calls", n)
	}
}

func TestOverviewReportsDatabaseTrouble(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, settings: `{"forms":[{"form_slug":"fw"}]}`,
		answer: func(c hostCall, out any) error {
			if c.fn == "db.query" {
				return errors.New("secret internals")
			}
			return nil
		}}
	f.install()
	res := serveReq(t, "GET", "/overview", nil)
	if res.Status != http.StatusServiceUnavailable || res.Body == "" || strings.Contains(res.Body, "secret") {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
}

func TestPercentOf(t *testing.T) {
	for _, c := range [][3]int{{1, 3, 33}, {2, 3, 67}, {0, 0, 0}, {5, 5, 100}, {7, 5, 100}} {
		if got := percentOf(c[0], c[1]); got != c[2] {
			t.Errorf("percentOf(%d,%d) = %d", c[0], c[1], got)
		}
	}
}
