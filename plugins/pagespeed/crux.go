package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
)

const cruxEndpoint = "https://chromeuxreport.googleapis.com/v1/records:queryRecord"

// Field statuses.
const (
	fieldOK    = "ok"
	fieldNone  = "none"
	fieldError = "error"
)

// fieldResult is the CrUX p75 of the three Core Web Vitals; a nil value was
// not reported. LCP and INP are milliseconds.
type fieldResult struct {
	Status        string
	LCP, INP, CLS *float64
}

// queryCrux asks the Chrome UX Report for a page URL, or the whole origin.
// formFactor is "PHONE" or "DESKTOP". A 404 means "not enough data".
func queryCrux(key, target, formFactor string, origin bool) (fieldResult, error) {
	subject := "url"
	if origin {
		subject = "origin"
	}
	body, _ := json.Marshal(map[string]any{
		subject:      target,
		"formFactor": formFactor,
		"metrics":    []string{"largest_contentful_paint", "interaction_to_next_paint", "cumulative_layout_shift"},
	})
	res, err := fetch(key, "POST", cruxEndpoint+"?key="+url.QueryEscape(key), string(body))
	if err != nil {
		return fieldResult{Status: fieldError}, err
	}
	switch res.Status {
	case http.StatusOK:
	case http.StatusNotFound:
		return fieldResult{Status: fieldNone}, nil
	default:
		return fieldResult{Status: fieldError}, fmt.Errorf("the Chrome UX Report answered HTTP %d", res.Status)
	}
	var doc struct {
		Record struct {
			Metrics map[string]struct {
				Percentiles struct {
					P75 json.RawMessage `json:"p75"`
				} `json:"percentiles"`
			} `json:"metrics"`
		} `json:"record"`
	}
	if err := json.Unmarshal([]byte(res.Body), &doc); err != nil {
		return fieldResult{Status: fieldError}, fmt.Errorf("the Chrome UX Report answer is not JSON")
	}
	p75 := func(name string) *float64 {
		raw := doc.Record.Metrics[name].Percentiles.P75
		var f float64
		if json.Unmarshal(raw, &f) == nil {
			return &f
		}
		var s string // CLS arrives as a string
		if json.Unmarshal(raw, &s) == nil {
			if f, err := strconv.ParseFloat(s, 64); err == nil {
				return &f
			}
		}
		return nil
	}
	f := fieldResult{
		Status: fieldOK,
		LCP:    p75("largest_contentful_paint"),
		INP:    p75("interaction_to_next_paint"),
		CLS:    p75("cumulative_layout_shift"),
	}
	if f.LCP == nil && f.INP == nil && f.CLS == nil {
		f.Status = fieldNone
	}
	return f, nil
}

// formFactor maps a strategy to CrUX's name for it.
func formFactor(strategy string) string {
	if strategy == strategyDesktop {
		return "DESKTOP"
	}
	return "PHONE"
}
