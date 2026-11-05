package main

import (
	"encoding/json"
	"errors"
	"math"
	"net/url"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	defaultCount   = 12
	maxCount       = 30
	importsPerStep = 3
	stepBudget     = 6 * time.Second // stop importing early; a step is cut at 10 s
	maxPages       = 5
)

// cursor is the run checkpoint: I counts raw search results walked so far, N
// the photos staged so far, Missing the first image hosts that were not ticked.
type cursor struct {
	I       int      `json:"i"`
	N       int      `json:"n"`
	Missing []string `json:"m,omitempty"`
}

type params struct {
	Query, Provider, Orientation string
	Count                        int
}

// bufferItem is what the fetch phase leaves for the transform phase.
type bufferItem struct {
	SourceKey string `json:"source_key"`
	Handle    string `json:"handle"`
	Alt       string `json:"alt"`
	Credit    string `json:"credit"`
	Caption   string `json:"caption"`
}

// parseParams reads the owner's inputs; a problem is returned as the message
// to show.
func parseParams(raw json.RawMessage) (params, string) {
	m := map[string]any{}
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &m); err != nil {
			m = map[string]any{}
		}
	}
	str := func(k string) string { s, _ := m[k].(string); return strings.TrimSpace(s) }
	p := params{Query: cleanText(str("query"), 200), Provider: str("provider"), Orientation: str("orientation"), Count: defaultCount}
	if p.Query == "" {
		return p, "Enter a search term."
	}
	switch p.Provider {
	case "":
		p.Provider = "pexels"
	case "pexels", "openverse":
	default:
		return p, "Unknown provider."
	}
	switch p.Orientation {
	case "", "any", "landscape", "portrait", "square":
	default:
		p.Orientation = "any"
	}
	if f, ok := m["count"].(float64); ok && !math.IsNaN(f) {
		p.Count = int(math.Max(1, math.Min(maxCount, math.Floor(f))))
	}
	return p, ""
}

// pexelsKey reads the write-only setting; it never leaves this process except
// in the Authorization header.
func pexelsKey() (string, error) {
	var s map[string]any
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return "", err
	}
	k, _ := s["pexels_api_key"].(string)
	return strings.TrimSpace(k), nil
}

// fetchStep searches one page, imports up to importsPerStep photos and always
// ends with a checkpoint, a finish or a failure.
func fetchStep(s sdk.RunStep) (sdk.RunStepResult, error) {
	info, err := guest.RunInfo(callHost)
	if err != nil {
		return failFromError(err)
	}
	p, msg := parseParams(info.Params)
	if msg != "" {
		return failRun(msg)
	}
	var cur cursor
	if s.Cursor != "" {
		if err := json.Unmarshal([]byte(s.Cursor), &cur); err != nil || cur.I < 0 || cur.N < 0 {
			return failRun("The run state could not be read. Start the run again.")
		}
	}

	pageSize, key := openversePageSize, ""
	if p.Provider == "pexels" {
		pageSize = pexelsPageSize
		if key, err = pexelsKey(); err != nil {
			return failFromError(err)
		}
		if key == "" {
			return failRun(msgKey)
		}
	}
	page := cur.I/pageSize + 1
	sp := searchParams{Query: p.Query, Provider: p.Provider, Orientation: p.Orientation, Page: page}
	var (
		photos []photo
		more   bool
		raw    int
	)
	if p.Provider == "pexels" {
		photos, more, raw, err = searchPexels(key, sp)
	} else {
		photos, more, raw, err = searchOpenverse(sp)
	}
	if err != nil {
		return failFromError(err)
	}

	start := time.Now()
	i, imports := cur.I%pageSize, 0
	for ; i < raw && i < len(photos) && imports < importsPerStep && cur.N < p.Count && time.Since(start) < stepBudget; i++ {
		cur.I++
		ph := photos[i]
		if ph.Skip {
			continue
		}
		sk := ph.Provider + ":" + ph.ID
		if !hostCovered(ph.ImageURL, info.Hosts) {
			host := hostOf(ph.ImageURL)
			cur.noteMissing(host)
			if err := putSkip(sk, "host not ticked: "+host); err != nil {
				return failFromError(err)
			}
			continue
		}
		imports++
		handle, err := guest.MediaImportURL(callHost, ph.ImageURL, ph.Filename)
		if err != nil {
			if code := tolerated(err); code != "" {
				if err := putSkip(sk, "import failed: "+code); err != nil {
					return failFromError(err)
				}
				continue
			}
			return failFromError(err)
		}
		val, _ := json.Marshal(bufferItem{SourceKey: sk, Handle: handle, Alt: ph.Alt, Credit: ph.Credit, Caption: ph.Caption})
		if err := guest.BufferPut(callHost, "item/"+sk, val); err != nil {
			return failFromError(err)
		}
		cur.N++
	}
	exhausted := i >= raw && !more
	if i >= raw && more {
		cur.I = page * pageSize // a short page: continue at the next page
	}

	if cur.N >= p.Count || exhausted || cur.I >= maxPages*pageSize {
		if cur.N == 0 {
			return failRun(noPhotosMessage(p.Query, cur))
		}
		_ = guest.RunProgress(callHost, cur.N, p.Count, "Fetched "+strconv.Itoa(cur.N)+" of "+strconv.Itoa(p.Count))
		return sdk.RunStepResult{}, orFail(guest.RunFinish(callHost, nil))
	}
	_ = guest.RunProgress(callHost, cur.N, p.Count, "Fetched "+strconv.Itoa(cur.N)+" of "+strconv.Itoa(p.Count))
	next, _ := json.Marshal(cur)
	if err := guest.RunCheckpoint(callHost, string(next)); err != nil {
		return failFromError(err)
	}
	return sdk.RunStepResult{}, nil
}

// orFail turns a failed finish call into a failed run.
func orFail(err error) error {
	if err != nil {
		_, _ = failFromError(err)
	}
	return nil
}

func (c *cursor) noteMissing(host string) {
	if host == "" || len(c.Missing) >= 3 {
		return
	}
	for _, m := range c.Missing {
		if m == host {
			return
		}
	}
	c.Missing = append(c.Missing, host)
}

func noPhotosMessage(query string, c cursor) string {
	switch {
	case len(c.Missing) > 0:
		return "No usable photos. Tick these hosts when you start the run: " + strings.Join(c.Missing, ", ") + "."
	case c.I == 0:
		return `No photos found for "` + query + `".`
	}
	return `No usable photos for "` + query + `".`
}

func hostOf(rawURL string) string {
	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}
	return strings.ToLower(u.Hostname())
}

func putSkip(key, reason string) error {
	val, _ := json.Marshal(map[string]string{"reason": reason})
	return guest.BufferPut(callHost, "skip/"+key, val)
}

// tolerated returns the code of a per-photo import failure that only skips
// that photo, "" for anything that must end the run.
func tolerated(err error) string {
	var he *sdk.HostError
	if !errors.As(err, &he) {
		return ""
	}
	switch he.Code {
	case sdk.ErrBlocked, sdk.ErrUnavailable, sdk.ErrInvalidArgument, sdk.ErrNotFound, sdk.ErrTimeout:
		return he.Code
	}
	return ""
}

// failRun ends the run with a message for the owner. It never returns an
// error: the host sees a clean step and the failed run.
func failRun(msg string) (sdk.RunStepResult, error) {
	_ = guest.RunFail(callHost, cleanText(msg, 500))
	return sdk.RunStepResult{}, nil
}

// failFromError ends the run for a provider or host error.
func failFromError(err error) (sdk.RunStepResult, error) {
	var pe *providerError
	if errors.As(err, &pe) {
		return failRun(pe.Message)
	}
	return failRun("Stock photo search failed.")
}
