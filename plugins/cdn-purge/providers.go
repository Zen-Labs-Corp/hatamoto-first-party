package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const (
	cloudflareBase = "https://api.cloudflare.com/client/v4"
	bunnyBase      = "https://api.bunny.net"
	fastlyBase     = "https://api.fastly.com"

	// cloudflareBatch is the most files one Cloudflare purge_cache call takes.
	cloudflareBatch = 30
	// callTimeout caps one provider request.
	callTimeout = 3 * time.Second
	// minCall is the least time left for which a request is started.
	minCall = 500 * time.Millisecond
	// errBodyChars is how much of a provider answer an error text keeps.
	errBodyChars = 120
)

// budgetEnd, when set, is the time by which the running hook must be done:
// fetch never starts a request that cannot finish before it and cuts the
// request timeout to what is left.
var budgetEnd time.Time

var errBudget = errors.New("out of time for this run")

// batchResult is the outcome of one provider request: the URLs it covered
// (empty for "everything"), the HTTP status (0 when no answer came) and an
// error text, empty on success.
type batchResult struct {
	URLs   []string
	Status int
	Err    string
}

func (r batchResult) ok() bool { return r.Err == "" }

type fetched struct {
	Status int    `json:"status"`
	Body   string `json:"body"`
}

// fetch calls the host's http.fetch once, with no retry. Credentials go in
// headers only; headers are never logged.
func fetch(method, rawURL string, headers map[string]string, body string) (fetched, error) {
	timeout := callTimeout
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
	return res, err
}

func truncate(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// redact hides the token in text. It runs before any truncation, so a token cut
// in half by the limit cannot survive.
func redact(text, token string) string {
	if token == "" {
		return text
	}
	return strings.ReplaceAll(text, token, "***")
}

// call runs one request and reduces the answer to a batchResult.
func call(s settings, method, rawURL string, headers map[string]string, body string, urls []string) batchResult {
	res, err := fetch(method, rawURL, headers, body)
	out := batchResult{URLs: urls, Status: res.Status}
	switch {
	case err != nil:
		out.Err = truncate(redact(err.Error(), s.APIToken), maxErrChars)
	case res.Status < 200 || res.Status > 299:
		out.Err = truncate(strings.TrimSpace(fmt.Sprintf("%d %s",
			res.Status, truncate(redact(res.Body, s.APIToken), errBodyChars))), maxErrChars)
	}
	return out
}

// authHeaders are the request headers for the provider, token included.
func authHeaders(s settings) map[string]string {
	switch s.Provider {
	case providerCloudflare:
		return map[string]string{"Authorization": "Bearer " + s.APIToken, "Content-Type": "application/json"}
	case providerBunny:
		return map[string]string{"AccessKey": s.APIToken, "Accept": "application/json"}
	case providerFastly:
		return map[string]string{"Fastly-Key": s.APIToken, "Accept": "application/json"}
	}
	return nil
}

// chunk splits urls into slices of at most n.
func chunk(urls []string, n int) [][]string {
	var out [][]string
	for len(urls) > n {
		out = append(out, urls[:n])
		urls = urls[n:]
	}
	if len(urls) > 0 {
		out = append(out, urls)
	}
	return out
}

// purgeURLs purges absolute URLs: Cloudflare in calls of up to 30 files, Bunny
// and Fastly one call per URL. When the time budget runs out, the rest is one
// "skipped" result. A 401 or 403 answer ends the run: the rest would fail the
// same way.
func purgeURLs(s settings, urls []string) []batchResult {
	var batches [][]string
	if s.Provider == providerCloudflare {
		batches = chunk(urls, cloudflareBatch)
	} else {
		batches = chunk(urls, 1)
	}
	var results []batchResult
	for i, b := range batches {
		r := purgeBatch(s, b)
		if r.Err == errBudget.Error() {
			var rest []string
			for _, left := range batches[i:] {
				rest = append(rest, left...)
			}
			results = append(results, batchResult{URLs: rest, Err: fmt.Sprintf("skipped %d URLs: out of time", len(rest))})
			break
		}
		results = append(results, r)
		if r.Status == http.StatusUnauthorized || r.Status == http.StatusForbidden {
			break // the token is refused; the other URLs would be refused too
		}
	}
	return results
}

func purgeBatch(s settings, urls []string) batchResult {
	h := authHeaders(s)
	switch s.Provider {
	case providerCloudflare:
		body, _ := json.Marshal(map[string]any{"files": urls})
		return call(s, "POST", cloudflareBase+"/zones/"+url.PathEscape(s.ZoneID)+"/purge_cache", h, string(body), urls)
	case providerBunny:
		return call(s, "POST", bunnyBase+"/purge?url="+url.QueryEscape(urls[0]), h, "", urls)
	case providerFastly:
		path, err := fastlyPurgePath(urls[0])
		if err != nil {
			return batchResult{URLs: urls, Err: err.Error()}
		}
		return call(s, "POST", fastlyBase+"/purge/"+path, h, "", urls)
	}
	return batchResult{URLs: urls, Err: "unknown provider"}
}

// fastlyPurgePath turns an absolute URL into the host+path that follows
// /purge/. The path is escaped, dot segments are encoded and the query is
// escaped into the path, so nothing in the URL can change the API request. The
// fragment is dropped.
func fastlyPurgePath(raw string) (string, error) {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("not a purgeable URL: %q", truncate(raw, 80))
	}
	segs := strings.Split(u.EscapedPath(), "/")
	for i, seg := range segs {
		if seg == "." || seg == ".." {
			segs[i] = strings.ReplaceAll(seg, ".", "%2e")
		}
	}
	p := u.Host + strings.Join(segs, "/")
	if u.RawQuery != "" {
		p += "%3F" + url.PathEscape(u.RawQuery)
	}
	return p, nil
}

// purgeAll drops the whole CDN cache of the configured zone or service.
func purgeAll(s settings) batchResult {
	h := authHeaders(s)
	switch s.Provider {
	case providerCloudflare:
		return call(s, "POST", cloudflareBase+"/zones/"+url.PathEscape(s.ZoneID)+"/purge_cache", h, `{"purge_everything":true}`, nil)
	case providerBunny:
		return call(s, "POST", bunnyBase+"/pullzone/"+url.PathEscape(s.PullZoneID)+"/purgeCache", h, "", nil)
	case providerFastly:
		return call(s, "POST", fastlyBase+"/service/"+url.PathEscape(s.ServiceID)+"/purge_all", h, "", nil)
	}
	return batchResult{Err: "unknown provider"}
}

// testConnection makes one read-only call that proves the token works.
func testConnection(s settings) (status int, msg string) {
	var rawURL string
	switch s.Provider {
	case providerCloudflare:
		rawURL = cloudflareBase + "/user/tokens/verify"
	case providerBunny:
		rawURL = bunnyBase + "/pullzone/" + url.PathEscape(s.PullZoneID)
	case providerFastly:
		rawURL = fastlyBase + "/service/" + url.PathEscape(s.ServiceID)
	default:
		return 0, "unknown provider"
	}
	r := call(s, "GET", rawURL, authHeaders(s), "", nil)
	if r.ok() {
		return r.Status, "token accepted"
	}
	return r.Status, r.Err
}
