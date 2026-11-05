package main

import (
	"strings"
	"testing"
)

func valid() monitorInput {
	return monitorInput{Name: "Shop", URL: "https://example.org/health", Interval: "15m", Keyword: "ok", MaxMS: 2000}
}

func TestValidateMonitorAccepts(t *testing.T) {
	m, err := validateMonitor(valid())
	if err != nil {
		t.Fatal(err)
	}
	if m.Name != "Shop" || m.IntervalS != 900 || m.Keyword != "ok" || m.MaxMS != 2000 || m.IsSite {
		t.Fatalf("got %+v", m)
	}
	in := monitorInput{Name: " Site ", URL: " http://example.org:80/ ", IsSite: true}
	m, err = validateMonitor(in)
	if err != nil || m.IntervalS != 300 || m.Name != "Site" || m.URL != "http://example.org:80/" || !m.IsSite || m.MaxMS != 0 {
		t.Fatalf("defaults: %+v %v", m, err)
	}
	for every, secs := range map[string]int{"1m": 60, "5m": 300, "15m": 900} {
		in := valid()
		in.Interval = every
		if m, err := validateMonitor(in); err != nil || m.IntervalS != secs {
			t.Fatalf("%s: %+v %v", every, m, err)
		}
	}
}

func TestValidateMonitorRejects(t *testing.T) {
	cases := map[string]func(*monitorInput){
		"empty name":     func(i *monitorInput) { i.Name = "  " },
		"long name":      func(i *monitorInput) { i.Name = strings.Repeat("a", 61) },
		"empty url":      func(i *monitorInput) { i.URL = "" },
		"ftp":            func(i *monitorInput) { i.URL = "ftp://example.org/" },
		"no scheme":      func(i *monitorInput) { i.URL = "example.org" },
		"no host":        func(i *monitorInput) { i.URL = "https:///path" },
		"credentials":    func(i *monitorInput) { i.URL = "https://user:pass@example.org/" },
		"user only":      func(i *monitorInput) { i.URL = "https://user@example.org/" },
		"long url":       func(i *monitorInput) { i.URL = "https://example.org/" + strings.Repeat("a", 500) },
		"space in url":   func(i *monitorInput) { i.URL = "https://example.org/a b" },
		"bad interval":   func(i *monitorInput) { i.Interval = "30s" },
		"long keyword":   func(i *monitorInput) { i.Keyword = strings.Repeat("k", 201) },
		"max_ms too low": func(i *monitorInput) { i.MaxMS = 99 },
		"max_ms too big": func(i *monitorInput) { i.MaxMS = 30001 },
		"negative max":   func(i *monitorInput) { i.MaxMS = -1 },
	}
	for name, mutate := range cases {
		in := valid()
		mutate(&in)
		if _, err := validateMonitor(in); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
	in := valid()
	in.MaxMS = 100
	if _, err := validateMonitor(in); err != nil {
		t.Errorf("max_ms 100: %v", err)
	}
	in.MaxMS = 30000
	if _, err := validateMonitor(in); err != nil {
		t.Errorf("max_ms 30000: %v", err)
	}
}

func TestValidateRejectsNUL(t *testing.T) {
	for name, in := range map[string]monitorInput{
		"name":    {Name: "a\x00b", URL: "https://example.org/"},
		"url":     {Name: "a", URL: "https://example.org/\x00"},
		"keyword": {Name: "a", URL: "https://example.org/", Keyword: "k\x00"},
	} {
		if _, err := validateMonitor(in); err == nil {
			t.Errorf("%s: NUL accepted", name)
		}
	}
}

func TestDecodeStrict(t *testing.T) {
	var in monitorInput
	if err := decodeStrict(`{"name":"a","url":"https://example.org"}`, &in); err != nil || in.Name != "a" {
		t.Fatalf("valid: %v %+v", err, in)
	}
	for name, body := range map[string]string{
		"unknown key": `{"name":"a","colour":"red"}`,
		"wrong type":  `{"name":5}`,
		"not json":    `nope`,
		"trailing":    `{"name":"a"} {"name":"b"}`,
		"empty":       ``,
	} {
		if err := decodeStrict(body, &monitorInput{}); err == nil {
			t.Errorf("%s: accepted", name)
		}
	}
}

func TestCheckCapacity(t *testing.T) {
	var twenty []monitor
	for i := range 20 {
		twenty = append(twenty, monitor{ID: int64(i + 1)})
	}
	if err := checkCapacity(twenty[:19], monitor{}); err != nil {
		t.Fatalf("20th monitor: %v", err)
	}
	if err := checkCapacity(twenty, monitor{}); err != errMax20 {
		t.Fatalf("21st monitor: %v", err)
	}
	if err := checkCapacity(twenty, monitor{ID: 5}); err != nil {
		t.Fatalf("updating one of 20: %v", err)
	}

	site := []monitor{{ID: 1, IsSite: true}, {ID: 2}}
	if err := checkCapacity(site, monitor{IsSite: true}); err != errTwoSites {
		t.Fatalf("second site: %v", err)
	}
	if err := checkCapacity(site, monitor{ID: 2, IsSite: true}); err != errTwoSites {
		t.Fatalf("promote second: %v", err)
	}
	if err := checkCapacity(site, monitor{ID: 1, IsSite: true}); err != nil {
		t.Fatalf("edit the site itself: %v", err)
	}
	if err := checkCapacity(site, monitor{}); err != nil {
		t.Fatalf("non-site: %v", err)
	}
}
