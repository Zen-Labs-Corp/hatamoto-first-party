package main

import (
	"errors"
	"net/mail"
	"slices"
	"strconv"
	"strings"

	"hatamoto.dev/sdk"
)

// otherOption is the stored option for an answer that is not among the
// question's options; it is shown as "Other".
const otherOption = "__other__"

type option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// fieldDef is one field of a core form, as forms.list returns it.
type fieldDef struct {
	Name    string   `json:"name"`
	Label   string   `json:"label"`
	Type    string   `json:"type"`
	Options []option `json:"options"`
}

// formDef is one core form: its name and fields.
type formDef struct {
	Slug   string     `json:"slug"`
	Name   string     `json:"name"`
	Fields []fieldDef `json:"fields"`
}

// listForms returns the forms core knows, by slug.
func listForms() (map[string]formDef, error) {
	var res struct {
		Items []formDef `json:"items"`
	}
	if err := callHost("forms.list", struct{}{}, &res); err != nil {
		return nil, err
	}
	out := make(map[string]formDef, len(res.Items))
	for _, f := range res.Items {
		out[f.Slug] = f
	}
	return out, nil
}

// choiceFields keeps the questions that are tallied: select, radio and
// checkbox fields with options. Other fields only count toward the total.
func choiceFields(fields []fieldDef) []fieldDef {
	var out []fieldDef
	for _, f := range fields {
		switch f.Type {
		case "select", "radio", "checkbox":
			if len(f.Options) > 0 {
				out = append(out, f)
			}
		}
	}
	return out
}

// answers returns the submitted values of one field: a string is one value, a
// list holds one per chosen option; anything else (a ticked box, a number, no
// answer) has none.
func answers(v any) []string {
	switch x := v.(type) {
	case string:
		if x == "" {
			return nil
		}
		return []string{x}
	case []any:
		var out []string
		for _, e := range x {
			if s, ok := e.(string); ok && s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	return nil
}

// countFor returns the (field, option) pairs one submission chose, each once,
// so a bulk upsert never sees a key twice. Values outside the options count
// under otherOption.
func countFor(fields []fieldDef, data map[string]any) map[[2]string]int {
	out := map[[2]string]int{}
	for _, f := range fields {
		for _, v := range answers(data[f.Name]) {
			opt := otherOption
			if slices.ContainsFunc(f.Options, func(o option) bool { return o.Value == v }) {
				opt = v
			}
			out[[2]string{f.Name, opt}] = 1
		}
	}
	return out
}

// scoreAnswers is answers for scoring: a number (core sends float64) or a
// ticked box also compares as text, so a key of 8 or true can match.
func scoreAnswers(v any) []string {
	switch x := v.(type) {
	case float64:
		return []string{strconv.FormatFloat(x, 'f', -1, 64)}
	case bool:
		return []string{strconv.FormatBool(x)}
	}
	return answers(v)
}

func norm(s string) string { return strings.ToLower(strings.TrimSpace(s)) }

// normSet lowercases, trims, sorts and de-duplicates values.
func normSet(vals []string) []string {
	var out []string
	for _, v := range vals {
		if n := norm(v); n != "" {
			out = append(out, n)
		}
	}
	slices.Sort(out)
	return slices.Compact(out)
}

// score counts the answer key entries one submission got right. The maximum is
// the number of entries. A checkbox key may list several values separated by
// commas and then needs exactly that set; other fields compare one trimmed,
// case-insensitive value. A key naming an unknown field never matches.
func score(cfg formCfg, fields []fieldDef, data map[string]any) (got, max int) {
	for _, k := range cfg.Key {
		max++
		i := slices.IndexFunc(fields, func(f fieldDef) bool { return f.Name == k.Field })
		if i < 0 {
			continue
		}
		given := scoreAnswers(data[k.Field])
		if fields[i].Type == "checkbox" {
			if want := normSet(strings.Split(k.Value, ",")); len(want) > 0 && slices.Equal(normSet(given), want) {
				got++
			}
		} else if len(given) == 1 && norm(k.Value) != "" && norm(given[0]) == norm(k.Value) {
			got++
		}
	}
	return got, max
}

// upsertSubmission records one submission and its choices in a single
// statement: the response row is inserted once per submission id and only a
// new row bumps the tallies, so a redelivered event changes nothing and two
// concurrent submissions never overwrite each other. It returns how many
// response rows it inserted (0 or 1); the CTEs run even when the tally list is
// empty.
const upsertSubmission = `WITH r AS (
  INSERT INTO poll_response (form, submission_id, score, max_score, choices)
  VALUES ($1::text, $2::text, $3::int, $4::int, $5::jsonb)
  ON CONFLICT DO NOTHING RETURNING 1),
t AS (
  INSERT INTO poll_tally (form, field, option, n)
  SELECT $1::text, t.field, t.option, 1
  FROM jsonb_to_recordset($5::jsonb) AS t(field text, option text), r
  ON CONFLICT (form, field, option) DO UPDATE SET n = poll_tally.n + 1)
SELECT to_jsonb(count(*)) FROM r`

type tallyRow struct {
	Field  string `json:"field"`
	Option string `json:"option"`
}

// sortedRows lists the counted pairs in a stable order.
func sortedRows(m map[[2]string]int) []tallyRow {
	rows := make([]tallyRow, 0, len(m))
	for k := range m {
		rows = append(rows, tallyRow{k[0], k[1]})
	}
	slices.SortFunc(rows, func(a, b tallyRow) int {
		if c := strings.Compare(a.Field, b.Field); c != 0 {
			return c
		}
		return strings.Compare(a.Option, b.Option)
	})
	return rows
}

// onSubmitted counts one form submission. It never fails the submission: every
// problem is logged and dropped, and Recount repairs a missed tally. Core never
// announces spam, so nothing is filtered here.
func onSubmitted(e sdk.FormSubmittedEvent) error {
	cfgs, err := loadConfig()
	if err != nil {
		_ = logf("warn", "polls: settings unreadable, submission not counted")
		return nil
	}
	i := slices.IndexFunc(cfgs, func(c formCfg) bool { return c.Slug == e.FormSlug })
	if i < 0 {
		return nil
	}
	cfg := cfgs[i]
	at := e.SubmittedAt
	if at.IsZero() {
		at = now()
	}
	if !cfg.open(at) {
		return nil
	}
	forms, err := listForms()
	if err != nil {
		_ = logf("warn", "polls: forms unreadable, submission not counted")
		return nil
	}
	form, ok := forms[cfg.Slug]
	if !ok {
		_ = logf("warn", "polls: form %q not found, submission not counted", cfg.Slug)
		return nil
	}
	var sc, mx any
	got, max := 0, 0
	if cfg.Kind == kindQuiz {
		got, max = score(cfg, form.Fields, e.Fields)
		sc, mx = got, max
	}
	rows := sortedRows(countFor(choiceFields(form.Fields), e.Fields))
	var inserted int
	if err := dbJSON(upsertSubmission, &inserted, cfg.Slug, e.SubmissionID, sc, mx, rows); err != nil {
		_ = logf("warn", "polls: submission not counted: %v", err)
		return nil
	}
	// A redelivered event finds its row and must not mail the score again.
	if inserted > 0 && cfg.Kind == kindQuiz && cfg.EmailScore && max > 0 {
		mailScore(form.Fields, e.Fields, got, max)
	}
	return nil
}

// recipient finds the submitter's address: the first filled email field, else
// the field named email. Only a plain address is returned.
func recipient(fields []fieldDef, data map[string]any) string {
	for _, match := range []func(fieldDef) bool{
		func(f fieldDef) bool { return f.Type == "email" },
		func(f fieldDef) bool { return f.Name == "email" },
	} {
		for _, f := range fields {
			if v := answers(data[f.Name]); match(f) && len(v) > 0 {
				return plainAddress(strings.TrimSpace(v[0]))
			}
		}
	}
	return ""
}

// plainAddress returns s when it is one bare address (no display name, no
// comment, no line breaks), else "".
func plainAddress(s string) string {
	if strings.ContainsAny(s, "\r\n") {
		return ""
	}
	if a, err := mail.ParseAddress(s); err != nil || a.Address != s {
		return ""
	}
	return s
}

// mailScore sends the score once. Any failure, a rate limit included, is logged
// by code only and dropped: no retry, and the tally stays.
func mailScore(fields []fieldDef, data map[string]any, got, max int) {
	to := recipient(fields, data)
	if to == "" {
		return
	}
	err := callHost("mail.send", map[string]any{
		"to":      []string{to},
		"subject": "Your quiz score",
		"text":    "You scored " + strconv.Itoa(got) + " of " + strconv.Itoa(max) + ".",
	}, nil)
	if err == nil {
		return
	}
	code := "error"
	var he *sdk.HostError
	if errors.As(err, &he) {
		code = he.Code
	}
	_ = logf("warn", "polls: score email not sent (%s)", code)
}
