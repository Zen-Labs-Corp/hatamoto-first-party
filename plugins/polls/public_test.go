package main

import (
	"errors"
	"net/http"
	"strings"
	"testing"
	"time"
)

func publicPage(t *testing.T, settings, forms string, answer func(hostCall, any) error) (status int, body string) {
	t.Helper()
	f := &fake{t: t, settings: settings, forms: forms, answer: answer}
	f.install()
	res := serveReq(t, "GET", "/results/{form_slug}", map[string]string{"form_slug": "fw"})
	if res.Status != http.StatusNoContent && res.Body != "" && !strings.HasPrefix(res.Headers["Content-Type"], "text/html") {
		t.Fatalf("content type %q", res.Headers["Content-Type"])
	}
	return res.Status, res.Body
}

func TestPublicAlwaysShowsEscapedBarsWithoutScript(t *testing.T) {
	forms := `{"items":[{"slug":"fw","name":"<b>Poll</b>","fields":[
 {"name":"fw","label":"<img src=x onerror=alert(1)>","type":"radio","options":[{"label":"<script>alert(1)</script>","value":"a"},{"label":"Fine & dandy","value":"b"}]}]}]}`
	status, body := publicPage(t, `{"forms":[{"form_slug":"fw","show_results":"always"}]}`, forms,
		answerResults(`{"responses":4,"tallies":[{"field":"fw","option":"a","n":3},{"field":"fw","option":"b","n":1}],"scores":[]}`))
	if status != http.StatusOK {
		t.Fatalf("status %d", status)
	}
	for _, bad := range []string{"<script", "<img", "<b>Poll", "http://", "https://"} {
		if strings.Contains(strings.ToLower(body), bad) {
			t.Errorf("page contains %q:\n%s", bad, body)
		}
	}
	for _, want := range []string{"&lt;script&gt;", "&lt;img src=x onerror=alert(1)&gt;", "Fine &amp; dandy", "75% (3)", "25% (1)", "4 responses", "width:75%"} {
		if !strings.Contains(body, want) {
			t.Errorf("page lacks %q", want)
		}
	}
}

func TestPublicGatingByShowResults(t *testing.T) {
	end := time.Date(2026, 11, 4, 12, 0, 0, 0, time.UTC)
	defer func() { now = time.Now }()
	ans := answerResults(resultsDoc)
	cases := []struct {
		name, setting string
		at            time.Time
		want          int
	}{
		{"always", `"show_results":"always"`, end, 200},
		{"never", `"show_results":"never"`, end, 404},
		{"default is never", `"kind":"poll"`, end, 404},
		{"after_close before", `"show_results":"after_close","closes_at":"2026-11-04T12:00"`, end.Add(-time.Second), 404},
		{"after_close at", `"show_results":"after_close","closes_at":"2026-11-04T12:00"`, end, 200},
		{"after_close, no close time", `"show_results":"after_close"`, end.Add(time.Hour), 404},
	}
	var hidden string
	for _, c := range cases {
		at := c.at
		now = func() time.Time { return at }
		status, body := publicPage(t, `{"forms":[{"form_slug":"fw",`+c.setting+`}]}`, formsJSON, ans)
		if status != c.want {
			t.Errorf("%s: status %d, want %d", c.name, status, c.want)
		}
		if c.want == 404 {
			hidden = body
			if !strings.Contains(body, "Results are not available.") {
				t.Errorf("%s: body %q", c.name, body)
			}
		}
	}
	// An unconfigured slug looks exactly like a hidden form.
	if status, body := publicPage(t, `{"forms":[]}`, formsJSON, ans); status != 404 || body != hidden {
		t.Fatalf("unconfigured: %d, same body %v", status, body == hidden)
	}
}

func TestPublicQuizShowsAverageOnly(t *testing.T) {
	status, body := publicPage(t, `{"forms":[{"form_slug":"fw","kind":"quiz","show_results":"always","answer_key":[{"field":"fw","value":"go"},{"field":"tools","value":"vim"}]}]}`,
		formsJSON, answerResults(resultsDoc))
	if status != 200 || !strings.Contains(body, "Average score 1.6 of 2") {
		t.Fatalf("%d %s", status, body)
	}
}

func TestPublicDatabaseTroubleIs503WithoutInternals(t *testing.T) {
	status, body := publicPage(t, `{"forms":[{"form_slug":"fw","show_results":"always"}]}`, formsJSON,
		func(c hostCall, out any) error { return errors.New("relation poll_tally does not exist") })
	if status != http.StatusServiceUnavailable || strings.Contains(body, "poll_tally") || strings.Contains(body, "relation") {
		t.Fatalf("%d %s", status, body)
	}
}
