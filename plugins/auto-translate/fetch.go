package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	stepBudget    = 8500 * time.Millisecond
	maxCheckpoint = 4096
	maxProgress   = 512
	maxFailReason = 500
)

// fetchStat is what one fetch step reports to the transform phase.
type fetchStat struct {
	Translated   int            `json:"translated"`
	Skipped      map[string]int `json:"skipped"`
	GlossaryLost int            `json:"glossary_lost"`
}

// failRun fails the run with a one-line reason. The step itself then ends
// normally: an error returned from a step is the host's way of failing a run,
// and it carries no reason the owner can read.
func failRun(reason string) (sdk.RunStepResult, error) {
	reason = strings.Join(strings.Fields(reason), " ")
	if len(reason) > maxFailReason {
		reason = cutBytes(reason, maxFailReason)
	}
	if err := guest.RunFail(callHost, reason); err != nil {
		return sdk.RunStepResult{}, err
	}
	return sdk.RunStepResult{}, nil
}

// fetchSetup is everything a fetch step derives from the run inputs and the
// settings. It is rebuilt on every step, so a resumed run needs no state.
type fetchSetup struct {
	p      params
	s      settings
	tr     translator
	target string // provider code of the target locale
	lim    limits
}

// prepare validates the run inputs and settings before any content is read or
// any provider is called. Its errors are safe to show to the owner.
func prepare(info sdk.RunInfo) (fetchSetup, error) {
	p, err := parseParams(info.Params)
	if err != nil {
		return fetchSetup{}, err
	}
	if p.Locale, err = normalizeLocale(p.Locale); err != nil {
		return fetchSetup{}, err
	}
	if p.Schema != "page" && p.Schema != "post" {
		return fetchSetup{}, errors.New("the content type must be page or post")
	}
	s, err := loadSettings()
	if err != nil {
		return fetchSetup{}, err
	}
	if err := s.configured(); err != nil {
		return fetchSetup{}, err
	}
	var target string
	if s.Provider == providerGoogle {
		target, err = googleLang(p.Locale)
	} else {
		target, err = deeplTarget(p.Locale)
	}
	if err != nil {
		return fetchSetup{}, err
	}
	host, err := providerHost(s, info.Hosts)
	if err != nil {
		return fetchSetup{}, err
	}
	return fetchSetup{p: p, s: s, tr: newTranslator(s, host), target: target, lim: limitsFor(s.Provider)}, nil
}

// sourceCode is the provider code of the entities' language when they all
// share one, and "" (the provider detects it) otherwise.
func (f fetchSetup) sourceCode(es []entity) string {
	if len(es) == 0 {
		return ""
	}
	loc := strings.ToLower(es[0].Locale)
	for _, e := range es[1:] {
		if strings.ToLower(e.Locale) != loc {
			return ""
		}
	}
	norm, err := normalizeLocale(loc)
	if err != nil {
		return ""
	}
	if f.s.Provider == providerGoogle {
		code, _ := googleLang(norm)
		return code
	}
	return deeplSource(norm)
}

// fetchStep reads one slice of the published content, sends it to the
// provider in one request and stores the translated records in the run
// buffer. Every problem fails the run with a readable reason.
func fetchStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	budgetEnd = time.Now().Add(stepBudget)
	info, err := guest.RunInfo(callHost)
	if err != nil {
		return failRun("the run details could not be read")
	}
	var cur cursor
	if s.Cursor != "" {
		if err := json.Unmarshal([]byte(s.Cursor), &cur); err != nil {
			return failRun("the run cursor could not be read")
		}
	}
	setup, err := prepare(info)
	if err != nil {
		return failRun(err.Error())
	}
	fail := func(err error) (sdk.RunStepResult, error) {
		return failRun(redact(err.Error(), setup.s.APIKey))
	}

	cands, next, done, counts, err := nextCandidates(setup.p, cur, setup.lim.MaxCount)
	if err != nil {
		return fail(err)
	}
	stat := fetchStat{Skipped: counts}

	units := make([][]string, len(cands))
	slots := make([][]slot, len(cands))
	for i, e := range cands {
		units[i], slots[i] = unitFor(e, setup.s.Glossary)
	}
	var stored int
	if len(cands) > 0 {
		n, err := firstBatch(units, setup.lim)
		switch {
		case errors.Is(err, ErrUnitTooLarge):
			// The first entry alone cannot be sent: skip it and go on.
			stat.Skipped = cands[0].countsAt
			stat.Skipped["too_large"]++
			next.C, next.I = cur.C, cands[0].idx+1
			done = false
			cands = nil
		case err != nil:
			return fail(err)
		default:
			if n < len(cands) {
				// The rest waits for the next step.
				stat.Skipped = cands[n-1].countsAt
				next.C, next.I = cur.C, cands[n-1].idx+1
				done = false
			}
			cands, units, slots = cands[:n], units[:n], slots[:n]
		}
	}

	chars := 0
	if len(cands) > 0 {
		var texts []string
		for _, u := range units {
			for _, t := range u {
				chars += len(t)
			}
			texts = append(texts, u...)
		}
		out, err := setup.tr.translate(texts, setup.target, setup.sourceCode(cands))
		if errors.Is(err, errBudget) {
			// Too little step time is left for the provider: nothing was
			// stored, so the next step starts this slice again.
			return resumeAt(cur, fail)
		}
		if err != nil {
			return fail(err)
		}
		if len(out) != len(texts) {
			return failRun("the provider returned a different number of texts than were sent")
		}
		at := 0
		for i, e := range cands {
			rec, lost := fill(e, out[at:at+len(units[i])], slots[i], setup.s.Glossary)
			at += len(units[i])
			rec.TargetLocale = setup.p.Locale
			err := putRecord(cur.Seq+stored, rec)
			if errors.Is(err, errRecordTooLarge) {
				stat.Skipped["too_large"]++
				continue
			}
			if err != nil {
				return fail(err)
			}
			stored++
			stat.Translated++
			stat.GlossaryLost += lost
		}
	}

	raw, err := json.Marshal(stat)
	if err != nil {
		return failRun("the step statistics could not be encoded")
	}
	if err := guest.BufferPut(callHost, fmt.Sprintf("stat/%06d", cur.N), raw); err != nil {
		return fail(fmt.Errorf("the run buffer refused the statistics: %w", hostReason(err)))
	}

	next.Seq, next.N = cur.Seq+stored, cur.N+1
	cp, err := json.Marshal(next)
	if err != nil || len(cp) > maxCheckpoint {
		return failRun("the run cursor is too large")
	}
	if err := guest.RunCheckpoint(callHost, string(cp)); err != nil {
		return fail(fmt.Errorf("the checkpoint was refused: %w", hostReason(err)))
	}
	msg := fmt.Sprintf("batch %d: %d entities, %d chars", next.N, stored, chars)
	if err := guest.RunProgress(callHost, next.Seq, 0, cutBytes(msg, maxProgress)); err != nil {
		return fail(fmt.Errorf("the progress report was refused: %w", hostReason(err)))
	}
	if done {
		if err := guest.RunFinish(callHost, map[string]string{"phase": "fetch"}); err != nil {
			return fail(fmt.Errorf("the phase could not be finished: %w", hostReason(err)))
		}
	}
	return sdk.RunStepResult{}, nil
}

// resumeAt ends the step with the checkpoint it started from.
func resumeAt(cur cursor, fail func(error) (sdk.RunStepResult, error)) (sdk.RunStepResult, error) {
	cp, err := json.Marshal(cur)
	if err != nil {
		return failRun("the run cursor could not be encoded")
	}
	if err := guest.RunCheckpoint(callHost, string(cp)); err != nil {
		return fail(fmt.Errorf("the checkpoint was refused: %w", hostReason(err)))
	}
	return sdk.RunStepResult{}, nil
}
