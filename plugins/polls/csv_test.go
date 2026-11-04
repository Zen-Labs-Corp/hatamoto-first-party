package main

import (
	"encoding/csv"
	"net/http"
	"strings"
	"testing"
)

const hostileForms = `{"items":[{"slug":"fw","name":"Framework","fields":[
 {"name":"fw","label":"Pick, \"one\"\nplease","type":"radio","options":[{"label":"=1+1","value":"a"},{"label":"@cmd","value":"b"},{"label":"Plain","value":"c"}]}]}]}`

func TestCSVRowsQuotingAndFormulaGuard(t *testing.T) {
	f := &fake{t: t, forms: hostileForms, settings: `{"forms":[{"form_slug":"fw"}]}`,
		answer: answerResults(`{"responses":4,"tallies":[{"field":"fw","option":"a","n":2},{"field":"fw","option":"c","n":2}],"scores":[]}`)}
	f.install()
	res := serveReq(t, "GET", "/forms/{slug}/csv", map[string]string{"slug": "fw"})
	if res.Status != http.StatusOK {
		t.Fatalf("%d %s", res.Status, res.Body)
	}
	if res.Headers["Content-Type"] != "text/csv; charset=utf-8" || res.Headers["Content-Disposition"] != `attachment; filename="fw.csv"` {
		t.Fatalf("headers %v", res.Headers)
	}
	rows, err := csv.NewReader(strings.NewReader(res.Body)).ReadAll()
	if err != nil {
		t.Fatalf("not valid CSV: %v\n%s", err, res.Body)
	}
	if strings.Join(rows[0], ",") != "form,question,option,count,percent" {
		t.Fatalf("header %v", rows[0])
	}
	if len(rows) != 4 {
		t.Fatalf("rows %v", rows)
	}
	if rows[1][1] != "Pick, \"one\"\nplease" || rows[1][2] != "'=1+1" || rows[1][3] != "2" || rows[1][4] != "50" {
		t.Fatalf("row 1 %q", rows[1])
	}
	if rows[2][2] != "'@cmd" || rows[2][3] != "0" {
		t.Fatalf("row 2 %q", rows[2])
	}
}

func TestCSVQuizScoreRows(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, answer: answerResults(resultsDoc),
		settings: `{"forms":[{"form_slug":"fw","kind":"quiz","answer_key":[{"field":"fw","value":"go"},{"field":"tools","value":"vim"}]}]}`}
	f.install()
	res := serveReq(t, "GET", "/forms/{slug}/csv", map[string]string{"slug": "fw"})
	rows, err := csv.NewReader(strings.NewReader(res.Body)).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	var score [][]string
	for _, r := range rows {
		if r[1] == "score" {
			score = append(score, r)
		}
	}
	if len(score) != 3 || strings.Join(score[2], ",") != "fw,score,2,6,60" {
		t.Fatalf("score rows %v", score)
	}
}

func TestCSVUnknownSlugIs404AndNameIsSanitised(t *testing.T) {
	f := &fake{t: t, forms: formsJSON, settings: `{"forms":[{"form_slug":"fw"}]}`, answer: answerResults(resultsDoc)}
	f.install()
	if res := serveReq(t, "GET", "/forms/{slug}/csv", map[string]string{"slug": "nope"}); res.Status != http.StatusNotFound {
		t.Fatalf("status %d", res.Status)
	}
	for in, want := range map[string]string{"My Poll!": "mypoll", "a\"b\r\nc": "abc", "": "poll", "ok-1_x": "ok-1_x"} {
		if got := csvName(in); got != want {
			t.Errorf("csvName(%q) = %q", in, got)
		}
	}
}

func TestCSVScorePercentUsesScoredResponses(t *testing.T) {
	// 10 responses, 5 of them without a score: the share is of the 5.
	res := formResult{Slug: "q", Kind: kindQuiz, Responses: 10,
		Scores: []scoreResult{{Score: 0, Count: 3}, {Score: 1, Count: 2}}}
	out, err := renderCSV(res)
	if err != nil {
		t.Fatal(err)
	}
	rows, _ := csv.NewReader(strings.NewReader(out)).ReadAll()
	if rows[1][4] != "60" || rows[2][4] != "40" {
		t.Fatalf("rows %v", rows)
	}
}
