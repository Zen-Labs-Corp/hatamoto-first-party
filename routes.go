// Command searchconsole is the first-party Google Search Console plugin.
//
// The owner connects Google through the plugin's own routes (OAuth 2.0 with
// state and PKCE); the tokens are stored encrypted in the plugin's schema. The
// plugin lists the owner's sites, submits the sitemap, inspects URLs and, once
// an hour, imports the days of search performance it is missing into its own
// tables. Every call to Google goes through the host's http.fetch, which only
// reaches the hosts in plugin.json.
package main

import (
	"encoding/json"
	"errors"
	"html"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	uiPath   = "/admin/plugins/searchconsole/settings"
	stateTTL = 600 // seconds a started OAuth flow stays valid
)

func init() {
	guest.OnHTTP(serve)
	guest.OnCron("import", func(sdk.CronEvent) error {
		s, err := loadSettings()
		if err != nil {
			return err
		}
		_, err = runImport(s, time.Now().Add(importBudget))
		if errors.Is(err, errNotConnected) || errors.Is(err, errNoSite) {
			return nil // nothing to import yet
		}
		return err
	})
}

func serve(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	s, err := loadSettings()
	if err != nil {
		return fail(err)
	}
	switch r.Method + " " + r.Pattern {
	case "GET /status":
		return status(s)
	case "GET /oauth/start":
		return oauthStart(s, r)
	case "GET /oauth/callback":
		return oauthCallback(s, r)
	case "POST /disconnect":
		if err := disconnect(); err != nil {
			return fail(err)
		}
		return reply(http.StatusOK, map[string]bool{"ok": true})
	case "GET /sites":
		sites, err := listSites(s)
		if err != nil {
			return problem(err)
		}
		return reply(http.StatusOK, map[string]any{"sites": nonNilSites(sites)})
	case "POST /site":
		return pickSite(s, r)
	case "POST /sitemap":
		return sitemap(s, r)
	case "POST /inspect":
		return inspect(s, r)
	case "GET /performance":
		return performance(r)
	case "POST /import":
		res, err := runImport(s, time.Now().Add(importBudget))
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

// problem answers a failure the owner can act on (not connected, Google said
// no): the message is safe to show.
func problem(err error) (sdk.HTTPResponse, error) {
	logf("warn", "%v", err)
	status := http.StatusBadGateway
	if errors.Is(err, errNotConnected) || errors.Is(err, errNoSite) || errors.Is(err, errNoKeyMaterial) {
		status = http.StatusConflict
	}
	return reply(status, map[string]string{"error": err.Error()})
}

func invalid(msg string) (sdk.HTTPResponse, error) {
	return reply(http.StatusUnprocessableEntity, map[string]string{"error": msg})
}

func nonNilSites(s []siteEntry) []siteEntry {
	if s == nil {
		return []siteEntry{}
	}
	return s
}

// page is a small HTML document for the browser tabs of the OAuth flow (the
// host serves plugin HTML sandboxed, without scripts).
func page(status int, title, body string, headers map[string]string) sdk.HTTPResponse {
	doc := "<!doctype html><meta charset=utf-8><title>" + html.EscapeString(title) + "</title><body style=\"font:16px system-ui;margin:3em auto;max-width:34em\"><h1>" +
		html.EscapeString(title) + "</h1>" + body
	h := map[string]string{"Content-Type": "text/html; charset=utf-8"}
	for k, v := range headers {
		h[k] = v
	}
	return sdk.HTTPResponse{Status: status, Headers: h, Body: doc}
}

// --- status ------------------------------------------------------------------------------

func status(s settings) (sdk.HTTPResponse, error) {
	out := map[string]any{
		"configured":   len(s.missing()) == 0,
		"missing":      s.missing(),
		"redirect_uri": "",
		"connected":    false,
		"site_url":     "",
	}
	if s.PublicURL != "" {
		out["redirect_uri"] = s.redirectURI()
	}
	c, err := loadConnection()
	if err != nil {
		return fail(err)
	}
	if c != nil {
		out["connected"], out["site_url"], out["connected_at"], out["scope"] = true, c.SiteURL, c.ConnectedAt, c.Scope
	}
	if run, err := lastRun(); err == nil && run != nil {
		out["last_import"] = run
	}
	if d, err := lastImportedDay(); err == nil && d != "" {
		out["imported_through"] = d
	}
	if sm, err := lastSitemap(); err == nil && sm != nil {
		out["last_sitemap"] = sm
	}
	return reply(http.StatusOK, out)
}

// --- OAuth ---------------------------------------------------------------------------------

type oauthState struct {
	Verifier string `json:"verifier"`
	UserID   string `json:"user_id"`
}

// oauthStart begins the flow. Navigated to (Accept: text/html) it sends the
// browser on to Google; asked for JSON it returns the address.
func oauthStart(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	if m := s.missing(); len(m) > 0 {
		return reply(http.StatusConflict, map[string]any{"error": "fill in the plugin settings first", "missing": m})
	}
	if _, err := s.key(); err != nil {
		return problem(err)
	}
	state := randomToken(16)
	verifier, challenge := pkcePair()
	uid := ""
	if r.User != nil {
		uid = r.User.ID
	}
	err := callHost("kv.set", map[string]any{"key": "oauth:" + state, "value": oauthState{Verifier: verifier, UserID: uid}, "ttl_seconds": stateTTL}, nil)
	if err != nil {
		return fail(err)
	}
	target := authorizationURL(s, state, challenge)
	if strings.Contains(r.Headers["accept"], "text/html") {
		link := html.EscapeString(target)
		return page(http.StatusOK, "Connecting to Google",
			`<meta http-equiv="refresh" content="0;url=`+link+`"><p><a href="`+link+`">Continue to Google</a></p>`, map[string]string{"Cache-Control": "no-store"}), nil
	}
	return reply(http.StatusOK, map[string]string{"authorization_url": target})
}

// oauthCallback is where Google sends the owner back. It is public, so the
// one-time state (random, 10 minutes) is what proves this browser began the
// flow; PKCE binds the code to this plugin's verifier.
func oauthCallback(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	q := r.Query
	if e := first(q["error"]); e != "" {
		return page(http.StatusBadRequest, "Google did not connect", "<p>Google answered: "+html.EscapeString(e)+".</p><p><a href=\""+uiPath+"\">Back to the plugin</a></p>", nil), nil
	}
	state, code := first(q["state"]), first(q["code"])
	if state == "" || code == "" || len(state) > 64 {
		return page(http.StatusBadRequest, "Link not valid", "<p>This address is not a Google answer.</p>", nil), nil
	}
	var got struct {
		Found bool            `json:"found"`
		Value json.RawMessage `json:"value"`
	}
	if err := callHost("kv.get", map[string]string{"key": "oauth:" + state}, &got); err != nil {
		return fail(err)
	}
	// The state is single use: whatever happens next, it is gone.
	_ = callHost("kv.delete", map[string]string{"key": "oauth:" + state}, nil)
	var st oauthState
	if !got.Found || json.Unmarshal(got.Value, &st) != nil {
		return page(http.StatusBadRequest, "Link expired", "<p>Start the connection again from the plugin page.</p>", nil), nil
	}
	key, err := s.key()
	if err != nil {
		return problem(err)
	}
	tok, err := exchangeCode(s, code, st.Verifier)
	if err != nil {
		logf("warn", "code exchange: %v", err)
		return page(http.StatusBadGateway, "Google refused the code", "<p>"+html.EscapeString(err.Error())+"</p>", nil), nil
	}
	if tok.RefreshToken == "" {
		return page(http.StatusBadGateway, "No refresh token", "<p>Google did not grant offline access. Remove the app from your Google account's connected apps and connect again.</p>", nil), nil
	}
	if err := storeConnection(key, tok); err != nil {
		return fail(err)
	}
	logf("info", "connected to Google; the flow was started by user %q", st.UserID)
	return sdk.HTTPResponse{Status: http.StatusFound, Headers: map[string]string{"Location": uiPath, "Cache-Control": "no-store"}}, nil
}

func first(v []string) string {
	if len(v) == 0 {
		return ""
	}
	return v[0]
}

// --- site, sitemap, inspection ----------------------------------------------------------------

var errNoSite = errors.New("pick a Search Console property first")

func chosenSite() (string, error) {
	c, err := loadConnection()
	if err != nil {
		return "", err
	}
	if c == nil {
		return "", errNotConnected
	}
	if c.SiteURL == "" {
		return "", errNoSite
	}
	return c.SiteURL, nil
}

func pickSite(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var in struct {
		SiteURL string `json:"site_url"`
	}
	if json.Unmarshal([]byte(r.Body), &in) != nil || in.SiteURL == "" {
		return invalid("site_url is required")
	}
	sites, err := listSites(s)
	if err != nil {
		return problem(err)
	}
	if !slices.ContainsFunc(sites, func(e siteEntry) bool { return e.SiteURL == in.SiteURL }) {
		return invalid("that property is not in your Search Console account")
	}
	if err := chooseSite(in.SiteURL); err != nil {
		return fail(err)
	}
	return reply(http.StatusOK, map[string]string{"site_url": in.SiteURL})
}

func sitemap(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var in struct {
		SitemapURL string `json:"sitemap_url"`
	}
	_ = json.Unmarshal([]byte(r.Body), &in) // an empty body means the default
	if in.SitemapURL == "" {
		if s.PublicURL == "" {
			return invalid("sitemap_url is required (or set the public address in the plugin settings)")
		}
		in.SitemapURL = s.PublicURL + "/sitemap.xml"
	}
	if u, err := url.Parse(in.SitemapURL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("sitemap_url must be an http or https address")
	}
	site, err := chosenSite()
	if err != nil {
		return problem(err)
	}
	subErr := submitSitemap(s, site, in.SitemapURL)
	msg := ""
	if subErr != nil {
		msg = subErr.Error()
	}
	if err := dbExec(`INSERT INTO sitemap_submissions (site_url, sitemap_url, error) VALUES ($1, $2, $3)`, site, in.SitemapURL, msg); err != nil {
		logf("warn", "recording the sitemap submission: %v", err)
	}
	if subErr != nil {
		return problem(subErr)
	}
	return reply(http.StatusOK, map[string]string{"sitemap_url": in.SitemapURL, "site_url": site})
}

func inspect(s settings, r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	var in struct {
		URL string `json:"url"`
	}
	if json.Unmarshal([]byte(r.Body), &in) != nil || in.URL == "" {
		return invalid("url is required")
	}
	if u, err := url.Parse(in.URL); err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return invalid("url must be an http or https address")
	}
	site, err := chosenSite()
	if err != nil {
		return problem(err)
	}
	res, raw, err := inspectURL(s, site, in.URL)
	if err != nil {
		return problem(err)
	}
	if err := dbExec(`INSERT INTO inspections (site_url, url, verdict, coverage, result) VALUES ($1, $2, $3, $4, $5::jsonb)`,
		site, in.URL, res.Verdict, res.CoverageState, string(raw)); err != nil {
		logf("warn", "recording the inspection: %v", err)
	}
	return reply(http.StatusOK, res)
}

// --- performance -------------------------------------------------------------------------------

func performance(r sdk.HTTPRequest) (sdk.HTTPResponse, error) {
	days := 28
	if v, err := strconv.Atoi(first(r.Query["days"])); err == nil && v > 0 {
		days = min(v, 365)
	}
	queries, err := topBy("query", days, 20)
	if err != nil {
		return fail(err)
	}
	pages, err := topBy("page", days, 20)
	if err != nil {
		return fail(err)
	}
	return reply(http.StatusOK, map[string]any{"days": days, "queries": queries, "pages": pages})
}

// --- import ------------------------------------------------------------------------------------

// importResult is what one import run did.
type importResult struct {
	Days    []string `json:"days"`
	Rows    int      `json:"rows"`
	Pending int      `json:"pending"`
}

// dataLagDays is how far behind today Search Console's final data is.
const dataLagDays = 2

// importBudget is how long one import call works, measured from its start;
// the plugin's call limit is 10 s.
const importBudget = 7 * time.Second

// pageReserve is the least time left for which another page is requested.
const pageReserve = 2 * time.Second

// runImport imports the days of the last ImportDays days (ending dataLagDays
// ago) that are not stored yet, oldest first, page by page, until deadline.
// A day that does not fit in the time is left half done: its progress is
// stored and the next run resumes at the page where this one stopped. Days it
// did not finish are counted in Pending.
func runImport(s settings, deadline time.Time) (importResult, error) {
	res := importResult{Days: []string{}}
	budgetEnd = deadline
	defer func() { budgetEnd = time.Time{} }()
	site, err := chosenSite()
	if err != nil {
		return res, err
	}
	end := time.Now().UTC().AddDate(0, 0, -dataLagDays)
	start := end.AddDate(0, 0, -(s.ImportDays - 1))
	have, err := importedDays(start.Format("2006-01-02"))
	if err != nil {
		return res, err
	}
	var todo []string
	for d := start; !d.After(end); d = d.AddDate(0, 0, 1) {
		if day := d.Format("2006-01-02"); !have[day] {
			todo = append(todo, day)
		}
	}
	var runErr error
days:
	for i, day := range todo {
		prog, err := loadProgress(site, day)
		if err != nil {
			runErr, res.Pending = err, len(todo)-i
			break
		}
		for {
			if time.Until(deadline) < pageReserve {
				res.Pending = len(todo) - i
				break days
			}
			rows, last, err := queryPage(s, site, day, prog.NextRow)
			if err == nil {
				err = storeRows(rows)
			}
			if err != nil {
				runErr, res.Pending = err, len(todo)-i
				break days
			}
			prog = dayProgress{NextRow: prog.NextRow + len(rows), Stored: prog.Stored + len(rows)}
			res.Rows += len(rows)
			if last {
				if err := markDay(site, day, prog.Stored); err != nil {
					runErr, res.Pending = err, len(todo)-i
					break days
				}
				res.Days = append(res.Days, day)
				break
			}
			if err := saveProgress(site, day, prog); err != nil {
				runErr, res.Pending = err, len(todo)-i
				break days
			}
		}
	}
	if len(todo) > 0 {
		recordRun(len(res.Days), res.Rows, runErr)
	}
	return res, runErr
}
