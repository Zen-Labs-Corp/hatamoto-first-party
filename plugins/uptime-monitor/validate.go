package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/url"
	"strings"
	"unicode/utf8"
)

const (
	maxMonitors   = 20
	defaultEvery  = "5m"
	maxNameLen    = 60
	maxURLLen     = 500
	maxKeywordLen = 200
)

var (
	errMax20    = errors.New("at most 20 monitors")
	errTwoSites = errors.New("only one monitor can be This site")
)

// intervals maps the accepted interval names to seconds.
var intervals = map[string]int{"1m": 60, "5m": 300, "15m": 900}

// monitorInput is a monitor as the admin page sends it.
type monitorInput struct {
	Name     string `json:"name"`
	URL      string `json:"url"`
	Interval string `json:"interval"`
	Keyword  string `json:"keyword"`
	MaxMS    int    `json:"max_ms"`
	IsSite   bool   `json:"is_site"`
}

// decodeStrict decodes one JSON value into v and rejects unknown keys and
// trailing data.
func decodeStrict(body string, v any) error {
	dec := json.NewDecoder(strings.NewReader(body))
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		return fmt.Errorf("invalid JSON: %w", err)
	}
	if _, err := dec.Token(); err != io.EOF {
		return errors.New("invalid JSON: trailing data")
	}
	return nil
}

// validateMonitor checks in and returns the monitor to store. The error text
// is shown to the owner.
func validateMonitor(in monitorInput) (monitor, error) {
	name := strings.TrimSpace(in.Name)
	if n := utf8.RuneCountInString(name); n < 1 || n > maxNameLen {
		return monitor{}, errors.New("name must be 1 to 60 characters")
	}
	if strings.ContainsRune(in.Name+in.URL+in.Keyword, 0) {
		return monitor{}, errors.New("name, url and keyword must not contain a NUL character")
	}
	if err := validateURL(strings.TrimSpace(in.URL)); err != nil {
		return monitor{}, err
	}
	every := in.Interval
	if every == "" {
		every = defaultEvery
	}
	secs, ok := intervals[every]
	if !ok {
		return monitor{}, errors.New("interval must be 1m, 5m or 15m")
	}
	if utf8.RuneCountInString(in.Keyword) > maxKeywordLen {
		return monitor{}, errors.New("keyword must be at most 200 characters")
	}
	if in.MaxMS != 0 && (in.MaxMS < 100 || in.MaxMS > 30000) {
		return monitor{}, errors.New("slow above must be 100 to 30000 ms")
	}
	return monitor{
		Name: name, URL: strings.TrimSpace(in.URL), IntervalS: secs,
		Keyword: in.Keyword, MaxMS: in.MaxMS, IsSite: in.IsSite,
	}, nil
}

func validateURL(raw string) error {
	if raw == "" || len(raw) > maxURLLen {
		return errors.New("url must be 1 to 500 characters")
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" {
		return errors.New("url must be an http or https address")
	}
	if u.User != nil || strings.Contains(u.Host, "@") {
		return errors.New("url must not contain a user name or password")
	}
	if strings.ContainsAny(raw, " \t\r\n") {
		return errors.New("url must not contain spaces")
	}
	return nil
}

// checkCapacity applies the limits that depend on the other monitors: at most
// 20 in total and one This site. existing is the current list; m is the
// monitor being stored, with ID set when it replaces an existing one.
func checkCapacity(existing []monitor, m monitor) error {
	others := 0
	for _, e := range existing {
		if e.ID == m.ID && m.ID != 0 {
			continue
		}
		others++
		if m.IsSite && e.IsSite {
			return errTwoSites
		}
	}
	if others >= maxMonitors {
		return errMax20
	}
	return nil
}
