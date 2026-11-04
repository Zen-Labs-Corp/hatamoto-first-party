package main

import (
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

// importNowBudget is how long POST /import-now works; the route's limit is 10 s.
const importNowBudget = 8500 * time.Millisecond

func init() {
	guest.OnHTTP(serve)
}

// serve answers the owner-only admin routes. None is public.
func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	switch r.Method + " " + r.Pattern {
	case "GET /overview":
		return overview(s)
	case "POST /test":
		return checkKey(s)
	case "POST /submit-sitemap":
		return submitSitemap(s)
	case "POST /import-now":
		if s.APIKey == "" {
			return problem(errNoKey)
		}
		res, err := runImport(s, time.Now().Add(importNowBudget), time.Now)
		if err != nil {
			return problem(err)
		}
		return reply(http.StatusOK, res)
	}
	return reply(http.StatusNotFound, map[string]string{"error": "not found"})
}

func reply(status int, v any) (sdk.HTTPResponse, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return sdk.HTTPResponse{}, err
	}
	return sdk.HTTPResponse{Status: status, Headers: map[string]string{"Content-Type": "application/json"}, Body: string(b)}, nil
}

// fail logs err and answers 500 without details.
func fail(err error) (sdk.HTTPResponse, error) {
	logf("error", "%v", err)
	return reply(http.StatusInternalServerError, map[string]string{"error": "the request failed; see the server log"})
}

// problem answers a failure the owner can act on (no key, no site, Bing said
// no). The message is built from a status and Bing's message, never a URL, so
// it is safe to show.
func problem(err error) (sdk.HTTPResponse, error) {
	logf("warn", "%v", err)
	status := http.StatusBadGateway
	switch {
	case errors.Is(err, errNoKey), errors.Is(err, errSiteChoice):
		status = http.StatusConflict
	case errors.Is(err, errSiteInvalid):
		status = http.StatusUnprocessableEntity
	}
	return reply(status, map[string]string{"error": err.Error()})
}

// siteFor resolves the site within the route's time limit.
func siteFor(s settings) (site, source string, err error) {
	budgetEnd = time.Now().Add(importNowBudget)
	defer func() { budgetEnd = time.Time{} }()
	return resolveSite(s)
}

type issueOut struct {
	crawlOut
	Labels []string `json:"labels"`
}

func overview(s settings) (sdk.HTTPResponse, error) {
	if s.APIKey == "" {
		return reply(http.StatusOK, map[string]any{"api_key_set": false})
	}
	data, err := loadStored()
	if err != nil {
		return fail(err)
	}
	site, source, siteErr := siteFor(s)
	var totals struct {
		Clicks      int `json:"clicks"`
		Impressions int `json:"impressions"`
	}
	for _, d := range data.Traffic {
		totals.Clicks += d.Clicks
		totals.Impressions += d.Impressions
	}
	issues := make([]issueOut, len(data.CrawlIssues))
	for i, c := range data.CrawlIssues {
		issues[i] = issueOut{crawlOut: c, Labels: issueLabels(c.Issues)}
	}
	out := map[string]any{
		"api_key_set":     true,
		"site_url":        site,
		"site_source":     source,
		"last_import":     data.LastRun,
		"traffic":         data.Traffic,
		"totals":          totals,
		"top_queries":     data.TopQueries,
		"top_pages":       data.TopPages,
		"crawl_issues":    issues,
		"feeds":           data.Feeds,
		"last_submission": data.LastSubmission,
	}
	if siteErr != nil {
		out["site_error"] = siteErr.Error()
	}
	return reply(http.StatusOK, out)
}

// checkKey checks the stored key with GetUserSites. It stores nothing.
func checkKey(s settings) (sdk.HTTPResponse, error) {
	if s.APIKey == "" {
		return problem(errNoKey)
	}
	budgetEnd = time.Now().Add(importNowBudget)
	defer func() { budgetEnd = time.Time{} }()
	sites, err := getUserSites(s)
	if err != nil {
		return problem(err)
	}
	verified, siteOK := 0, false
	for _, u := range sites {
		if u.IsVerified {
			verified++
			siteOK = siteOK || (s.SiteURL != "" && u.URL == s.SiteURL)
		}
	}
	if s.SiteURL == "" {
		siteOK = verified == 1
	}
	return reply(http.StatusOK, map[string]any{"ok": true, "sites_verified": verified, "site_url_ok": siteOK})
}

// submitSitemap sends <site>/sitemap.xml to Bing and records the attempt.
func submitSitemap(s settings) (sdk.HTTPResponse, error) {
	site, _, err := siteFor(s)
	if err != nil {
		return problem(err)
	}
	feed, err := sitemapURL(site)
	if err != nil {
		return problem(err)
	}
	budgetEnd = time.Now().Add(importNowBudget)
	subErr := submitFeed(s, site, feed)
	budgetEnd = time.Time{}
	if err := recordSubmission(feed, time.Now(), subErr); err != nil {
		logf("warn", "sitemap log: %v", err)
	}
	if subErr != nil {
		return problem(subErr)
	}
	return reply(http.StatusOK, map[string]any{"ok": true, "feed_url": feed})
}
