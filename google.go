package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// Google endpoints. A manifest config key may override each one (tests, a
// proxy you package yourself): they are deliberately not owner-editable
// settings, since the client secret and the refresh token are sent to them.
// The manifest's allowed_hosts still decide what the plugin can reach.
const (
	defaultAuthURL     = "https://accounts.google.com/o/oauth2/v2/auth"
	defaultTokenURL    = "https://oauth2.googleapis.com/token"
	defaultAPIBase     = "https://www.googleapis.com"
	defaultInspectBase = "https://searchconsole.googleapis.com"
	oauthScope         = "https://www.googleapis.com/auth/webmasters"
)

// settings is the plugin's settings object (plugin.json settings_schema).
type settings struct {
	ClientID        string `json:"client_id"`
	ClientSecret    string `json:"client_secret"`
	PublicURL       string `json:"public_url"`
	EncryptionKey   string `json:"encryption_key"`
	ImportDays      int    `json:"import_days"`
	EndpointAuth    string `json:"-"` // manifest config "endpoint_auth"
	EndpointToken   string `json:"-"` // "endpoint_token"
	EndpointAPI     string `json:"-"` // "endpoint_api"
	EndpointInspect string `json:"-"` // "endpoint_inspect"
}

func loadSettings() (settings, error) {
	var s settings
	if err := callHost("settings.get", struct{}{}, &s); err != nil {
		return s, err
	}
	if s.ImportDays < 1 || s.ImportDays > 30 {
		s.ImportDays = 7
	}
	s.PublicURL = strings.TrimRight(strings.TrimSpace(s.PublicURL), "/")
	s.EndpointAuth, _ = config("endpoint_auth")
	s.EndpointToken, _ = config("endpoint_token")
	s.EndpointAPI, _ = config("endpoint_api")
	s.EndpointInspect, _ = config("endpoint_inspect")
	return s, nil
}

func or(v, def string) string {
	if v = strings.TrimRight(strings.TrimSpace(v), "/"); v != "" {
		return v
	}
	return def
}

func (s settings) authURL() string     { return or(s.EndpointAuth, defaultAuthURL) }
func (s settings) tokenURL() string    { return or(s.EndpointToken, defaultTokenURL) }
func (s settings) apiBase() string     { return or(s.EndpointAPI, defaultAPIBase) }
func (s settings) inspectBase() string { return or(s.EndpointInspect, defaultInspectBase) }

// redirectURI is where Google sends the owner back.
func (s settings) redirectURI() string { return s.PublicURL + "/p/searchconsole/oauth/callback" }

// missing lists the settings still to fill in before connecting.
func (s settings) missing() []string {
	var m []string
	if s.ClientID == "" {
		m = append(m, "client_id")
	}
	if s.ClientSecret == "" {
		m = append(m, "client_secret")
	}
	if s.PublicURL == "" {
		m = append(m, "public_url")
	}
	return m
}

func (s settings) key() ([]byte, error) { return deriveKey(s.EncryptionKey, s.ClientSecret) }

// --- raw HTTP through the host -----------------------------------------------------

type fetched struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	Truncated bool   `json:"truncated"`
}

const (
	fetchTimeout = 8 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = time.Second
)

// budgetEnd, when set, is the time by which the running call must be done:
// fetch never starts a request that cannot finish before it and cuts the
// request timeout to what is left. The plugin's call limit is 10 s.
var budgetEnd time.Time

var errBudget = errors.New("out of time for this run; the rest continues next time")

// fetch calls the host's http.fetch. An answer the host cut at its response
// cap (1 MiB) is an error: parsing half a JSON document only hides the cause.
func fetch(method, rawURL string, headers map[string]string, body string) (fetched, error) {
	timeout := fetchTimeout
	if !budgetEnd.IsZero() {
		left := time.Until(budgetEnd)
		if left < minCall {
			return fetched{}, errBudget
		}
		timeout = min(timeout, left)
	}
	var res fetched
	err := callHost("http.fetch", map[string]any{
		"method": method, "url": rawURL, "headers": headers, "body": body, "timeout_ms": int(timeout / time.Millisecond),
	}, &res)
	if err == nil && res.Truncated {
		return res, errors.New("Google's answer is larger than the 1 MiB a plugin may receive; ask for fewer rows")
	}
	return res, err
}

// googleError is a Google error answer reduced to its message. The APIs answer
// {"error":{"message":..}}, the token endpoint {"error":"..","error_description":".."}.
func googleError(status int, body string) error {
	var api struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	var oauth struct {
		Error       string `json:"error"`
		Description string `json:"error_description"`
	}
	msg := ""
	if json.Unmarshal([]byte(body), &api) == nil {
		msg = api.Error.Message
	}
	if msg == "" && json.Unmarshal([]byte(body), &oauth) == nil {
		msg = strings.TrimSpace(oauth.Error + " " + oauth.Description)
	}
	if msg == "" {
		msg = "no details"
	}
	return fmt.Errorf("Google answered HTTP %d: %s", status, msg)
}

// --- OAuth ---------------------------------------------------------------------------

type tokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	Scope        string `json:"scope"`
}

func tokenRequest(s settings, form url.Values) (tokenResponse, error) {
	var t tokenResponse
	form.Set("client_id", s.ClientID)
	form.Set("client_secret", s.ClientSecret)
	res, err := fetch("POST", s.tokenURL(), map[string]string{"Content-Type": "application/x-www-form-urlencoded", "Accept": "application/json"}, form.Encode())
	if err != nil {
		return t, err
	}
	if res.Status != 200 {
		return t, googleError(res.Status, res.Body)
	}
	if err := json.Unmarshal([]byte(res.Body), &t); err != nil || t.AccessToken == "" {
		return t, errors.New("Google's token answer was not understood")
	}
	return t, nil
}

func exchangeCode(s settings, code, verifier string) (tokenResponse, error) {
	return tokenRequest(s, url.Values{
		"grant_type": {"authorization_code"}, "code": {code}, "redirect_uri": {s.redirectURI()}, "code_verifier": {verifier},
	})
}

func refreshAccessToken(s settings, refresh string) (tokenResponse, error) {
	return tokenRequest(s, url.Values{"grant_type": {"refresh_token"}, "refresh_token": {refresh}})
}

func authorizationURL(s settings, state, challenge string) string {
	q := url.Values{
		"response_type": {"code"}, "client_id": {s.ClientID}, "redirect_uri": {s.redirectURI()},
		"scope": {oauthScope}, "access_type": {"offline"}, "prompt": {"consent"},
		"state": {state}, "code_challenge": {challenge}, "code_challenge_method": {"S256"},
	}
	sep := "?"
	if strings.Contains(s.authURL(), "?") {
		sep = "&"
	}
	return s.authURL() + sep + q.Encode()
}

// --- authorised API calls ----------------------------------------------------------------

var errNotConnected = errors.New("not connected to Google: connect first")

// accessToken returns a usable access token, refreshing it when it is about
// to expire (or when force is set, after a 401).
func accessToken(s settings, force bool) (string, error) {
	key, err := s.key()
	if err != nil {
		return "", err
	}
	c, err := loadConnection()
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", errNotConnected
	}
	if !force && c.AccessToken != "" && c.AccessExpiresAt != "" {
		if exp, err := time.Parse(time.RFC3339, c.AccessExpiresAt); err == nil && time.Until(exp) > time.Minute {
			if tok, err := open(key, c.AccessToken); err == nil {
				return tok, nil
			}
		}
	}
	refresh, err := open(key, c.RefreshToken)
	if err != nil {
		return "", err
	}
	t, err := refreshAccessToken(s, refresh)
	if err != nil {
		return "", err
	}
	if err := saveAccessToken(key, t); err != nil {
		return "", err
	}
	return t.AccessToken, nil
}

// google calls a Google API with the stored credentials. in is sent as JSON
// when not nil; the JSON answer goes to out when not nil. A 401 refreshes the
// token once.
func google(s settings, method, rawURL string, in, out any) error {
	// Compact JSON: Google pretty-prints by default, which doubles the size.
	if strings.Contains(rawURL, "?") {
		rawURL += "&prettyPrint=false"
	} else {
		rawURL += "?prettyPrint=false"
	}
	body := ""
	if in != nil {
		b, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = string(b)
	}
	for attempt := 0; ; attempt++ {
		tok, err := accessToken(s, attempt > 0)
		if err != nil {
			return err
		}
		headers := map[string]string{"Authorization": "Bearer " + tok, "Accept": "application/json"}
		if in != nil {
			headers["Content-Type"] = "application/json"
		}
		res, err := fetch(method, rawURL, headers, body)
		if err != nil {
			return err
		}
		if res.Status == 401 && attempt == 0 {
			continue
		}
		if res.Status < 200 || res.Status > 299 {
			return googleError(res.Status, res.Body)
		}
		if out != nil && strings.TrimSpace(res.Body) != "" {
			return json.Unmarshal([]byte(res.Body), out)
		}
		return nil
	}
}

func siteURL(s settings, site string) string {
	return s.apiBase() + "/webmasters/v3/sites/" + url.PathEscape(site)
}

type siteEntry struct {
	SiteURL         string `json:"siteUrl"`
	PermissionLevel string `json:"permissionLevel"`
}

func listSites(s settings) ([]siteEntry, error) {
	var out struct {
		SiteEntry []siteEntry `json:"siteEntry"`
	}
	if err := google(s, "GET", s.apiBase()+"/webmasters/v3/sites", nil, &out); err != nil {
		return nil, err
	}
	return out.SiteEntry, nil
}

func submitSitemap(s settings, site, sitemap string) error {
	return google(s, "PUT", siteURL(s, site)+"/sitemaps/"+url.PathEscape(sitemap), nil, nil)
}

// inspection is the part of the URL Inspection answer the page shows.
type inspection struct {
	Verdict         string `json:"verdict"`
	CoverageState   string `json:"coverage_state"`
	IndexingState   string `json:"indexing_state"`
	RobotsTxtState  string `json:"robots_txt_state"`
	LastCrawlTime   string `json:"last_crawl_time"`
	GoogleCanonical string `json:"google_canonical"`
	UserCanonical   string `json:"user_canonical"`
	CrawledAs       string `json:"crawled_as"`
}

func inspectURL(s settings, site, target string) (inspection, json.RawMessage, error) {
	var out struct {
		InspectionResult struct {
			IndexStatusResult struct {
				Verdict         string `json:"verdict"`
				CoverageState   string `json:"coverageState"`
				IndexingState   string `json:"indexingState"`
				RobotsTxtState  string `json:"robotsTxtState"`
				LastCrawlTime   string `json:"lastCrawlTime"`
				GoogleCanonical string `json:"googleCanonical"`
				UserCanonical   string `json:"userCanonical"`
				CrawledAs       string `json:"crawledAs"`
			} `json:"indexStatusResult"`
		} `json:"inspectionResult"`
	}
	var raw json.RawMessage
	err := google(s, "POST", s.inspectBase()+"/v1/urlInspection/index:inspect", map[string]string{"inspectionUrl": target, "siteUrl": site}, &raw)
	if err != nil {
		return inspection{}, nil, err
	}
	if err := json.Unmarshal(raw, &out); err != nil {
		return inspection{}, nil, err
	}
	r := out.InspectionResult.IndexStatusResult
	return inspection{
		Verdict: r.Verdict, CoverageState: r.CoverageState, IndexingState: r.IndexingState, RobotsTxtState: r.RobotsTxtState,
		LastCrawlTime: r.LastCrawlTime, GoogleCanonical: r.GoogleCanonical, UserCanonical: r.UserCanonical, CrawledAs: r.CrawledAs,
	}, raw, nil
}

// perfRow is one Search Analytics row for the dimensions date, query, page.
type perfRow struct {
	Day         string  `json:"day"`
	Query       string  `json:"query"`
	Page        string  `json:"page"`
	Clicks      int     `json:"clicks"`
	Impressions int     `json:"impressions"`
	CTR         float64 `json:"ctr"`
	Position    float64 `json:"avg_position"`
}

const (
	// A page of 1,000 rows is about 300 KiB: well inside the host's 1 MiB cap
	// (a 5,000-row page was not). A day has at most perfMaxPages pages.
	perfPageSize = 1000
	perfMaxPages = 100 // 100,000 rows a day; the rest of a huge site's tail is dropped
)

// queryPage fetches one page of the Search Analytics rows of a day, starting
// at row startRow. last reports that no page follows.
func queryPage(s settings, site, day string, startRow int) (rows []perfRow, last bool, err error) {
	var out struct {
		Rows []struct {
			Keys        []string `json:"keys"`
			Clicks      float64  `json:"clicks"`
			Impressions float64  `json:"impressions"`
			CTR         float64  `json:"ctr"`
			Position    float64  `json:"position"`
		} `json:"rows"`
	}
	err = google(s, "POST", siteURL(s, site)+"/searchAnalytics/query", map[string]any{
		"startDate": day, "endDate": day, "dimensions": []string{"date", "query", "page"},
		"rowLimit": perfPageSize, "startRow": startRow, "dataState": "final",
	}, &out)
	if err != nil {
		return nil, false, err
	}
	for _, r := range out.Rows {
		if len(r.Keys) != 3 {
			continue
		}
		rows = append(rows, perfRow{Day: r.Keys[0], Query: r.Keys[1], Page: r.Keys[2],
			Clicks: int(r.Clicks), Impressions: int(r.Impressions), CTR: r.CTR, Position: r.Position})
	}
	return rows, len(out.Rows) < perfPageSize || startRow+len(out.Rows) >= perfPageSize*perfMaxPages, nil
}
