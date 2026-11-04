package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
)

// defaultBase is the Bing Webmaster JSON API. A manifest config key
// "bing_base" may override it (tests, a proxy you package yourself); it is
// deliberately not an owner-editable setting, since the API key is sent to it.
// The manifest's allowed_hosts still decide what the plugin can reach.
const defaultBase = "https://ssl.bing.com/webmaster/api.svc/json/"

// settings is the plugin's settings object (plugin.json settings_schema).
type settings struct {
	APIKey  string `json:"api_key"`
	SiteURL string `json:"site_url"`
}

var (
	errNoKey     = errors.New("Add your Bing API key in Settings.")
	errKeyDenied = errors.New("Bing rejected the API key.")
	errBudget    = errors.New("out of time for this run; the rest continues next time")
)

func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return s, err
	}
	s.APIKey = strings.TrimSpace(s.APIKey)
	s.SiteURL = strings.TrimSpace(s.SiteURL)
	return s, nil
}

func base() string {
	if b, ok := config("bing_base"); ok && strings.TrimSpace(b) != "" {
		return strings.TrimRight(strings.TrimSpace(b), "/") + "/"
	}
	return defaultBase
}

const (
	fetchTimeout = 8 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = time.Second
)

// budgetEnd, when set, is the time by which the running call must be done:
// call never starts a request that cannot finish before it and cuts the
// request timeout to what is left. The plugin's call limit is 10 s.
var budgetEnd time.Time

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

var apikeyRx = regexp.MustCompile(`(?i)apikey=[^&\s"']*`)

// redact removes the API key (raw and URL-escaped) from text that came from
// outside the plugin, so no error can carry it.
func redact(text, key string) string {
	text = apikeyRx.ReplaceAllString(text, "[key]")
	if key == "" {
		return text
	}
	text = strings.ReplaceAll(text, key, "[key]")
	return strings.ReplaceAll(text, url.QueryEscape(key), "[key]")
}

// call runs one Bing method and returns the content of the answer's "d". The
// request URL carries the API key, so it is never put into an error or a log.
func call(s settings, method, name string, q url.Values, body any) (json.RawMessage, error) {
	if s.APIKey == "" {
		return nil, errNoKey
	}
	timeout := fetchTimeout
	if !budgetEnd.IsZero() {
		left := time.Until(budgetEnd)
		if left < minCall {
			return nil, errBudget
		}
		timeout = min(timeout, left)
	}
	v := url.Values{}
	for k, vals := range q {
		v[k] = vals
	}
	v.Set("apikey", s.APIKey)
	args := map[string]any{
		"method": method, "url": base() + name + "?" + v.Encode(),
		"headers":    map[string]string{"Accept": "application/json"},
		"timeout_ms": int(timeout / time.Millisecond),
	}
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		args["headers"] = map[string]string{"Accept": "application/json", "Content-Type": "application/json; charset=utf-8"}
		args["body"] = string(b)
	}
	var res fetched
	if err := callHost("http.fetch", args, &res); err != nil {
		return nil, errors.New(redact(err.Error(), s.APIKey))
	}
	if res.Truncated {
		return nil, fmt.Errorf("Bing's answer for %s was too large", name)
	}
	var env struct {
		D         json.RawMessage `json:"d"`
		Message   string          `json:"Message"`
		ErrorCode json.RawMessage `json:"ErrorCode"`
	}
	parsed := json.Unmarshal([]byte(res.Body), &env) == nil
	code := strings.Trim(string(env.ErrorCode), `"`)
	if code == "null" || code == "0" {
		code = ""
	}
	fault := parsed && (env.Message != "" || code != "")
	if res.Status < 200 || res.Status > 299 || (fault && env.D == nil) {
		msg := strings.TrimSpace(env.Message + " " + code)
		if !parsed {
			msg, code = "", ""
		}
		return nil, bingError(res.Status, msg, code, q.Get("siteUrl"), s.APIKey)
	}
	if !parsed {
		return nil, fmt.Errorf("Bing's answer for %s was not understood", name)
	}
	return env.D, nil
}

// errNoAccess is Bing saying the key is fine but not allowed for the site.
type errNoAccess struct{ site string }

func (e errNoAccess) Error() string {
	return "Bing says this key has no access to " + e.site + ". Check that Site URL matches the site exactly as verified in Bing (including the trailing slash)."
}

// stopsRun reports an error that makes every further call of a run pointless.
func stopsRun(err error) bool {
	var na errNoAccess
	return errors.Is(err, errKeyDenied) || errors.As(err, &na)
}

// Bing's ApiErrorCode values for a bad key and for a site the key may not use.
const (
	codeInvalidAPIKey = "3"
	codeNotAuthorized = "14"
)

// bingError maps a failed answer to text for the owner: status and Bing's
// message only. A bad key and a site the key has no access to are told apart.
func bingError(status int, message, code, site, key string) error {
	message = redact(strings.TrimSpace(message), key)
	low := strings.ToLower(message)
	switch {
	case strings.Contains(low, "invalidapikey") || code == codeInvalidAPIKey:
		return errKeyDenied
	case strings.Contains(low, "notauthorized") || code == codeNotAuthorized:
		if site == "" { // GetUserSites and the like: nothing but the key to blame
			return errKeyDenied
		}
		return errNoAccess{site}
	case status == 401 || status == 403:
		return errKeyDenied
	}
	if r := []rune(message); len(r) > 200 {
		message = string(r[:200])
	}
	if message == "" {
		message = "no details"
	}
	return fmt.Errorf("Bing returned %d: %s", status, message)
}

// decode reads the "d" of an answer into out; a missing or null d is empty.
func decode(name string, d json.RawMessage, out any) error {
	if len(d) == 0 || string(d) == "null" {
		return nil
	}
	if err := json.Unmarshal(d, out); err != nil {
		return fmt.Errorf("Bing's answer for %s was not understood", name)
	}
	return nil
}

// --- dates -------------------------------------------------------------------------------

var dateRx = regexp.MustCompile(`^/Date\((-?\d+)(?:([+-])(\d{2})(\d{2}))?\)/$`)

// parseBingInstant reads /Date(ms[+-hhmm])/ as the instant it names (UTC) and
// the offset it carries in minutes.
func parseBingInstant(s string) (time.Time, int, error) {
	m := dateRx.FindStringSubmatch(strings.TrimSpace(s))
	if m == nil {
		return time.Time{}, 0, fmt.Errorf("unknown date %q", s)
	}
	ms, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return time.Time{}, 0, err
	}
	off := 0
	if m[2] != "" {
		h, _ := strconv.Atoi(m[3])
		mi, _ := strconv.Atoi(m[4])
		off = h*60 + mi
		if m[2] == "-" {
			off = -off
		}
	}
	return time.UnixMilli(ms).UTC(), off, nil
}

// parseBingDate is Bing's local calendar day of a /Date()/ value: the instant
// shifted by its offset, as YYYY-MM-DD.
func parseBingDate(s string) (string, error) {
	t, off, err := parseBingInstant(s)
	if err != nil {
		return "", err
	}
	return t.Add(time.Duration(off) * time.Minute).Format("2006-01-02"), nil
}

// parseBingTime is the instant of a /Date()/ value; Bing's "never" (a year
// before 1970) is nil.
func parseBingTime(s string) *time.Time {
	t, _, err := parseBingInstant(s)
	if err != nil || t.Year() < 1970 {
		return nil
	}
	return &t
}

// --- endpoints ---------------------------------------------------------------------------

type userSite struct {
	URL        string `json:"Url"`
	IsVerified bool   `json:"IsVerified"`
}

func getUserSites(s settings) ([]userSite, error) {
	d, err := call(s, "GET", "GetUserSites", nil, nil)
	if err != nil {
		return nil, err
	}
	var out []userSite
	return out, decode("GetUserSites", d, &out)
}

type trafficRow struct {
	Day         string `json:"day"`
	Clicks      int    `json:"clicks"`
	Impressions int    `json:"impressions"`
}

func siteQuery(site string) url.Values { return url.Values{"siteUrl": {site}} }

func trafficStats(s settings, site string) ([]trafficRow, error) {
	d, err := call(s, "GET", "GetRankAndTrafficStats", siteQuery(site), nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Date        string
		Clicks      int
		Impressions int
	}
	if err := decode("GetRankAndTrafficStats", d, &raw); err != nil {
		return nil, err
	}
	rows := make([]trafficRow, 0, len(raw))
	for _, r := range raw {
		day, err := parseBingDate(r.Date)
		if err != nil {
			continue // a row without a usable date cannot be keyed
		}
		rows = append(rows, trafficRow{Day: day, Clicks: r.Clicks, Impressions: r.Impressions})
	}
	return rows, nil
}

// statRow is one weekly query or page row; Key is the query or the page URL.
type statRow struct {
	Key         string  `json:"key"`
	Week        string  `json:"week"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	AvgClickPos float64 `json:"avg_click_position"`
	AvgImprPos  float64 `json:"avg_impression_position"`
}

func queryStats(s settings, site string) ([]statRow, error) { return stats(s, "GetQueryStats", site) }
func pageStats(s settings, site string) ([]statRow, error)  { return stats(s, "GetPageStats", site) }

// stats reads GetQueryStats or GetPageStats; both put the query or the page
// URL in "Query".
func stats(s settings, name, site string) ([]statRow, error) {
	d, err := call(s, "GET", name, siteQuery(site), nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Query                 string
		Date                  string
		Clicks                int
		Impressions           int
		AvgClickPosition      float64
		AvgImpressionPosition float64
	}
	if err := decode(name, d, &raw); err != nil {
		return nil, err
	}
	rows := make([]statRow, 0, len(raw))
	for _, r := range raw {
		week, err := parseBingDate(r.Date)
		if err != nil || r.Query == "" {
			continue
		}
		rows = append(rows, statRow{Key: r.Query, Week: week, Clicks: r.Clicks, Impressions: r.Impressions,
			AvgClickPos: r.AvgClickPosition, AvgImprPos: r.AvgImpressionPosition})
	}
	return rows, nil
}

type crawlRow struct {
	URL      string `json:"url"`
	HTTPCode int    `json:"http_code"`
	Issues   int    `json:"issues"`
	InLinks  int    `json:"in_links"`
}

func crawlIssues(s settings, site string) ([]crawlRow, error) {
	d, err := call(s, "GET", "GetCrawlIssues", siteQuery(site), nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Url      string
		HttpCode int
		Issues   int
		InLinks  int
	}
	if err := decode("GetCrawlIssues", d, &raw); err != nil {
		return nil, err
	}
	rows := make([]crawlRow, 0, len(raw))
	for _, r := range raw {
		if r.Url != "" {
			rows = append(rows, crawlRow{URL: r.Url, HTTPCode: r.HttpCode, Issues: r.Issues, InLinks: r.InLinks})
		}
	}
	return rows, nil
}

type feedRow struct {
	URL           string     `json:"url"`
	Type          string     `json:"type"`
	Status        string     `json:"status"`
	SubmittedAt   *time.Time `json:"submitted_at"`
	LastCrawledAt *time.Time `json:"last_crawled_at"`
	URLCount      int        `json:"url_count"`
}

func feeds(s settings, site string) ([]feedRow, error) {
	d, err := call(s, "GET", "GetFeeds", siteQuery(site), nil)
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Url         string
		Type        string
		Status      string
		Submitted   string
		LastCrawled string
		UrlCount    int
	}
	if err := decode("GetFeeds", d, &raw); err != nil {
		return nil, err
	}
	rows := make([]feedRow, 0, len(raw))
	for _, r := range raw {
		if r.Url != "" {
			rows = append(rows, feedRow{URL: r.Url, Type: r.Type, Status: r.Status,
				SubmittedAt: parseBingTime(r.Submitted), LastCrawledAt: parseBingTime(r.LastCrawled), URLCount: r.UrlCount})
		}
	}
	return rows, nil
}

func submitFeed(s settings, site, feedURL string) error {
	_, err := call(s, "POST", "SubmitFeed", nil, map[string]string{"siteUrl": site, "feedUrl": feedURL})
	return err
}

const maxSiteURL = 2048

// parseSite checks a Site URL: http or https, a host, no credentials, no
// control characters, at most maxSiteURL bytes.
func parseSite(site string) (*url.URL, error) {
	if len(site) > maxSiteURL || strings.IndexFunc(site, func(r rune) bool { return unicode.IsControl(r) || unicode.IsSpace(r) }) >= 0 {
		return nil, errSiteInvalid
	}
	u, err := url.Parse(site)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil {
		return nil, errSiteInvalid
	}
	return u, nil
}

// sitemapURL is where core serves the sitemap: /sitemap.xml at the origin of
// the site, whatever path or query the Site URL carries.
func sitemapURL(site string) (string, error) {
	u, err := parseSite(site)
	if err != nil {
		return "", err
	}
	return (&url.URL{Scheme: u.Scheme, Host: u.Host, Path: "/sitemap.xml"}).String(), nil
}

// --- site --------------------------------------------------------------------------------

const siteCacheTTL = 600 // seconds a site resolved from Bing is remembered

var errSiteChoice = errors.New("Bing lists no single verified site for this key. Fill in Site URL in Settings, exactly as it is verified in Bing Webmaster Tools.")

// errSiteInvalid is an invalid Site URL setting.
var errSiteInvalid = errors.New("Site URL must be an http:// or https:// address, exactly as it is verified in Bing Webmaster Tools.")

func parseSiteOK(site string) bool { _, err := parseSite(site); return err == nil }

// keyTag is a short fingerprint of the key (never the key), stored with the
// cached site so that a site resolved for another key is not reused.
func keyTag(key string) string {
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:4])
}

// resolveSite returns the site to ask Bing about and where it came from
// ("setting" or "bing"). The Site URL is kept as written: Bing matches the
// verified string, a trailing slash included.
func resolveSite(s settings) (site, source string, err error) {
	if s.APIKey == "" {
		return "", "none", errNoKey
	}
	if s.SiteURL != "" {
		if _, err := parseSite(s.SiteURL); err != nil {
			return "", "none", err
		}
		return s.SiteURL, "setting", nil
	}
	var got struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if callHost("kv.get", map[string]string{"key": "site"}, &got) == nil && got.Found {
		var cached struct {
			Site string `json:"site"`
			Key  string `json:"key"`
		}
		if json.Unmarshal(got.Value, &cached) == nil && cached.Key == keyTag(s.APIKey) && parseSiteOK(cached.Site) {
			return cached.Site, "bing", nil
		}
	}
	sites, err := getUserSites(s)
	if err != nil {
		return "", "none", err
	}
	var verified []string
	for _, u := range sites {
		if u.IsVerified && parseSiteOK(u.URL) {
			verified = append(verified, u.URL)
		}
	}
	if len(verified) != 1 {
		return "", "none", errSiteChoice
	}
	_ = callHost("kv.set", map[string]any{"key": "site", "value": map[string]string{"site": verified[0], "key": keyTag(s.APIKey)}, "ttl_seconds": siteCacheTTL}, nil)
	return verified[0], "bing", nil
}

// --- crawl issue flags -------------------------------------------------------------------

var issueNames = []struct {
	flag int
	name string
}{
	{1, "301 redirect"}, {2, "302 redirect"}, {4, "4xx error"}, {8, "5xx error"},
	{16, "Blocked by robots.txt"}, {32, "Contains malware"}, {64, "Important URL blocked by robots.txt"},
	{128, "DNS error"}, {256, "Timeout"},
}

// issueLabels names the flags set in Bing's Issues value.
func issueLabels(flags int) []string {
	out := []string{}
	for _, n := range issueNames {
		if flags&n.flag != 0 {
			out = append(out, n.name)
		}
	}
	return out
}
