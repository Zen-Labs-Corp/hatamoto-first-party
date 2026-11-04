package main

import (
	"math"
	"net/http"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// otherLabel is how the catch-all option is shown.
const otherLabel = "Other"

type optionResult struct {
	Value   string `json:"value"`
	Label   string `json:"label"`
	Count   int    `json:"count"`
	Percent int    `json:"percent"`
	Correct bool   `json:"correct"`
}

type questionResult struct {
	Field   string         `json:"field"`
	Label   string         `json:"label"`
	Options []optionResult `json:"options"`
}

type scoreResult struct {
	Score int `json:"score"`
	Count int `json:"count"`
}

// formResult is everything the admin page, the CSV, the public page and the
// page builder source show about one form.
type formResult struct {
	Slug        string           `json:"slug"`
	Name        string           `json:"name"`
	Kind        string           `json:"kind"`
	ShowResults string           `json:"show_results"`
	ClosesAt    string           `json:"closes_at"`
	Responses   int              `json:"responses"`
	Questions   []questionResult `json:"questions"`
	Scores      []scoreResult    `json:"scores"`
	MaxScore    int              `json:"max_score"`
	Average     float64          `json:"average"`
	Note        string           `json:"note,omitempty"`
}

// qResults reads everything stored for one form as one jsonb document.
const qResults = `SELECT jsonb_build_object(
  'responses', (SELECT count(*) FROM poll_response WHERE form = $1::text),
  'tallies', (SELECT coalesce(jsonb_agg(jsonb_build_object('field', field, 'option', option, 'n', n)), '[]'::jsonb)
              FROM poll_tally WHERE form = $1::text),
  'scores', (SELECT coalesce(jsonb_agg(jsonb_build_object('score', score, 'n', n) ORDER BY score), '[]'::jsonb)
             FROM (SELECT score, count(*) AS n FROM poll_response
                   WHERE form = $1::text AND score IS NOT NULL GROUP BY score) s))`

// percentOf is the rounded whole-number share of n in total; 0 when total is 0.
func percentOf(n, total int) int {
	if total <= 0 {
		return 0
	}
	return min(100, (n*100+total/2)/total)
}

// isCorrect reports whether value is (one of) the correct answers of a quiz field.
func isCorrect(cfg formCfg, f fieldDef, value string) bool {
	if cfg.Kind != kindQuiz {
		return false
	}
	for _, k := range cfg.Key {
		if k.Field != f.Name {
			continue
		}
		want := []string{k.Value}
		if f.Type == "checkbox" {
			want = strings.Split(k.Value, ",")
		}
		if slices.Contains(normSet(want), norm(value)) {
			return true
		}
	}
	return false
}

// buildResults reads one form's stored counts and lays them out against the
// form's questions. Options with no answers are listed too; counts for values
// that are no longer options are shown under Other.
func buildResults(cfg formCfg, form formDef) (formResult, error) {
	var doc struct {
		Responses int `json:"responses"`
		Tallies   []struct {
			Field  string `json:"field"`
			Option string `json:"option"`
			N      int    `json:"n"`
		} `json:"tallies"`
		Scores []struct {
			Score int `json:"score"`
			N     int `json:"n"`
		} `json:"scores"`
	}
	if err := dbJSON(qResults, &doc, cfg.Slug); err != nil {
		return formResult{}, err
	}
	res := formResult{
		Slug: cfg.Slug, Name: form.Name, Kind: cfg.Kind, ShowResults: cfg.ShowResults, ClosesAt: cfg.ClosesAt,
		Responses: doc.Responses, Questions: []questionResult{}, Scores: []scoreResult{},
	}
	if res.Name == "" {
		res.Name = cfg.Slug
	}
	counts := map[[2]string]int{}
	for _, t := range doc.Tallies {
		counts[[2]string{t.Field, t.Option}] += t.N
	}
	for _, f := range choiceFields(form.Fields) {
		q := questionResult{Field: f.Name, Label: f.Label, Options: []optionResult{}}
		if q.Label == "" {
			q.Label = f.Name
		}
		known := map[string]bool{}
		sum := 0
		for _, o := range f.Options {
			known[o.Value] = true
			n := counts[[2]string{f.Name, o.Value}]
			sum += n
			label := o.Label
			if label == "" {
				label = o.Value
			}
			q.Options = append(q.Options, optionResult{Value: o.Value, Label: label, Count: n, Correct: isCorrect(cfg, f, o.Value)})
		}
		other := 0
		for k, n := range counts {
			if k[0] == f.Name && !known[k[1]] {
				other += n
			}
		}
		sum += other
		if other > 0 {
			q.Options = append(q.Options, optionResult{Value: otherOption, Label: otherLabel, Count: other})
		}
		total := sum
		if f.Type == "checkbox" {
			total = res.Responses
		}
		for i := range q.Options {
			q.Options[i].Percent = percentOf(q.Options[i].Count, total)
		}
		res.Questions = append(res.Questions, q)
	}
	if cfg.Kind == kindQuiz {
		res.MaxScore = len(cfg.Key)
		byScore := map[int]int{}
		scored, points := 0, 0
		for _, s := range doc.Scores {
			byScore[s.Score] += s.N
			scored += s.N
			points += s.Score * s.N
		}
		for s := 0; s <= res.MaxScore; s++ {
			res.Scores = append(res.Scores, scoreResult{Score: s, Count: byScore[s]})
		}
		var extra []int
		for s := range byScore {
			if s > res.MaxScore || s < 0 {
				extra = append(extra, s)
			}
		}
		slices.Sort(extra)
		for _, s := range extra {
			res.Scores = append(res.Scores, scoreResult{Score: s, Count: byScore[s]})
		}
		if scored > 0 {
			res.Average = math.Round(float64(points)/float64(scored)*10) / 10
		}
	}
	return res, nil
}

// handleOverview lists every configured form with its results. Forms removed
// from the setting are purged first; a purge failure is logged and ignored.
func handleOverview(sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	cfgs, err := loadConfig()
	if err != nil {
		_ = logf("warn", "polls: overview: settings unreadable")
		return fail(http.StatusServiceUnavailable, "settings unreadable")
	}
	if err := purge(cfgs); err != nil {
		_ = logf("warn", "polls: purge failed: %v", err)
	}
	out := struct {
		Forms []formResult `json:"forms"`
	}{Forms: []formResult{}}
	if len(cfgs) == 0 {
		return reply(http.StatusOK, out)
	}
	forms, err := listForms()
	if err != nil {
		_ = logf("warn", "polls: overview: forms unreadable")
		return fail(http.StatusServiceUnavailable, "forms unreadable")
	}
	for _, cfg := range cfgs {
		form, ok := forms[cfg.Slug]
		if !ok {
			out.Forms = append(out.Forms, formResult{
				Slug: cfg.Slug, Name: cfg.Slug, Kind: cfg.Kind, ShowResults: cfg.ShowResults, ClosesAt: cfg.ClosesAt,
				Questions: []questionResult{}, Scores: []scoreResult{}, Note: "form not found",
			})
			continue
		}
		res, err := buildResults(cfg, form)
		if err != nil {
			_ = logf("warn", "polls: overview of %q failed: %v", cfg.Slug, err)
			return fail(http.StatusServiceUnavailable, "results unavailable")
		}
		out.Forms = append(out.Forms, res)
	}
	return reply(http.StatusOK, out)
}
