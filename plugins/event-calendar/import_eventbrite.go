package main

import (
	"fmt"
	"net/url"
	"strings"
	"time"

	"encoding/json"
	"hatamoto.dev/sdk"
)

// The Eventbrite source reads the organization's live events through the API,
// one page a step. The private token comes from the top-level eventbrite_token
// setting (the only kind the host seals); it goes into one request header and
// nowhere else: never into a reason, a progress line, the summary or the buffer.

const (
	eventbriteAPI      = "https://www.eventbriteapi.com/v3/organizations/"
	maxEventbritePages = 20
	maxContinuation    = 512
)

// ebPage is the part of an organization's events answer the import reads.
type ebPage struct {
	Events []struct {
		ID   string `json:"id"`
		Name struct {
			Text string `json:"text"`
		} `json:"name"`
		Description struct {
			Text string `json:"text"`
		} `json:"description"`
		URL    string `json:"url"`
		Status string `json:"status"`
		Start  struct {
			UTC string `json:"utc"`
		} `json:"start"`
		End struct {
			UTC string `json:"utc"`
		} `json:"end"`
		Venue *struct {
			Name    string `json:"name"`
			Address struct {
				Address1 string `json:"address_1"`
				City     string `json:"city"`
			} `json:"address"`
		} `json:"venue"`
	} `json:"events"`
	Pagination struct {
		ObjectCount  *int   `json:"object_count"`
		HasMore      bool   `json:"has_more_items"`
		Continuation string `json:"continuation"`
	} `json:"pagination"`
}

// eventbriteToken reads the token; empty when none is set.
func eventbriteToken() string {
	var raw struct {
		Token string `json:"eventbrite_token"`
	}
	_ = callHost(sdk.FnSettingsGet, struct{}{}, &raw)
	return strings.TrimSpace(raw.Token)
}

// fetchEventbritePage reads one page of an organization's events. It returns
// the events, the continuation of the next page ("" after the last) and a
// short reason without the address or the token when the page was not read.
func fetchEventbritePage(orgID, cont, token string) ([]importEvent, string, string) {
	events, next, _, why := fetchEventbritePageWithin(orgID, cont, token, importFetchMs)
	return events, next, why
}

// fetchEventbritePageWithin is fetchEventbritePage with the fetch timeout in
// milliseconds. total is the API's own count of the organization's events, -1
// when the answer carries none.
func fetchEventbritePageWithin(orgID, cont, token string, timeoutMs int) ([]importEvent, string, int, string) {
	if token == "" {
		return nil, "", -1, "no Eventbrite token is set"
	}
	addr := eventbriteAPI + url.PathEscape(orgID) + "/events/?status=live&time_filter=current_future&expand=venue&page_size=50"
	if cont != "" {
		addr += "&continuation=" + url.QueryEscape(cont)
	}
	var res fetched
	err := callHost(sdk.FnHTTPFetch, map[string]any{
		"method": "GET", "url": addr, "timeout_ms": timeoutMs,
		"headers": map[string]string{"Authorization": "Bearer " + token, "Accept": "application/json", "User-Agent": "hatamoto-event-calendar"},
	}, &res)
	switch {
	case err != nil:
		return nil, "", -1, "the address could not be fetched"
	case res.Truncated:
		return nil, "", -1, "the answer was too large"
	case res.Status == 401 || res.Status == 403:
		return nil, "", -1, "the Eventbrite token was refused"
	case res.Status != 200:
		return nil, "", -1, fmt.Sprintf("the answer was status %d", res.Status)
	}
	var page ebPage
	if json.Unmarshal([]byte(res.Body), &page) != nil {
		return nil, "", -1, "the answer was not Eventbrite data"
	}
	var out []importEvent
	for _, e := range page.Events {
		start, err := time.Parse(time.RFC3339, e.Start.UTC)
		title := cleanText(e.Name.Text, maxImportTitle, false)
		if err != nil || e.ID == "" || title == "" {
			continue
		}
		ev := importEvent{
			UID: "eventbrite:" + e.ID, Title: title, Start: start,
			Description: cleanText(e.Description.Text, maxImportText, true),
		}
		if end, err := time.Parse(time.RFC3339, e.End.UTC); err == nil && !end.Before(start) {
			ev.End = end
		}
		if isAbsoluteURL(e.URL) && len(e.URL) <= maxImportURLLen {
			ev.TicketURL = e.URL
		}
		if e.Status == "canceled" {
			ev.Status = "cancelled"
		}
		if v := e.Venue; v != nil {
			ev.Venue = cleanText(v.Name, maxImportTitle, false)
			ev.Address = cleanText(v.Address.Address1, maxImportTitle, false)
			ev.City = cleanText(v.Address.City, maxImportTitle, false)
		}
		out = append(out, ev)
	}
	next := ""
	if page.Pagination.HasMore && len(page.Pagination.Continuation) <= maxContinuation {
		next = page.Pagination.Continuation
	}
	total := -1
	if n := page.Pagination.ObjectCount; n != nil && *n >= 0 {
		total = *n
	}
	return out, next, total, ""
}
