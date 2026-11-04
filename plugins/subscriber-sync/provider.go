package main

import "encoding/json"

// job is one queued submission to deliver.
type job struct {
	SubmissionID string `json:"submission_id"`
	FormSlug     string `json:"form_slug"`
	Email        string `json:"email"`
	FirstName    string `json:"first_name"`
	LastName     string `json:"last_name"`
	Attempts     int    `json:"attempts"`
}

// fetchReq is one provider request.
type fetchReq struct {
	Method  string
	URL     string
	Headers map[string]string
	Body    string
}

// fetchResp is the answer: an HTTP status and body, or a host error code
// (Status 0). Raw host error text is never kept.
type fetchResp struct {
	Status int
	Body   string
	Code   string
}

// fetcher sends a request; doFetch in production, a fake in tests.
type fetcher func(fetchReq) fetchResp

// Result kinds.
const (
	kindSent    = "sent"
	kindUpdated = "updated"
	kindRetry   = "retry"
	kindFail    = "fail"
	// kindBudget: the time budget ran out before the delivery was done. The
	// provider did not fail, so it is neither logged nor counted as an attempt.
	kindBudget = "budget"
)

// result is the outcome of one delivery. reason is a fixed short word, never
// provider text.
type result struct {
	kind   string
	status int
	reason string
}

// provider talks to one email service. Every upsert is safe to repeat.
type provider interface {
	upsert(j job, c cfg, f fetcher) result
	test(c cfg, f fetcher) (listName string, r result)
}

// providerFor is nil for an unknown provider name.
func providerFor(name string) provider {
	switch name {
	case "mailchimp":
		return mailchimp{}
	case "brevo":
		return brevo{}
	case "mailerlite":
		return mailerlite{}
	case "kit":
		return kit{}
	}
	return nil
}

// maxPages bounds how many pages a lookup reads.
const maxPages = 5

// deliver sends one job through the configured provider.
func deliver(c cfg, j job) result {
	p := providerFor(c.Provider)
	if p == nil {
		return result{kind: kindFail, reason: "not configured"}
	}
	return p.upsert(j, c, doFetch)
}

// classify turns a non-2xx status into an outcome: 408, 429 and 5xx are tried
// again later, 401 and 403 mean the key is wrong, any other status is a
// rejection that retrying cannot fix.
func classify(status int) result {
	switch {
	case status == 408 || status == 429 || status >= 500:
		return result{kind: kindRetry, status: status}
	case status == 401 || status == 403:
		return result{kind: kindFail, status: status, reason: "unauthorized"}
	}
	return result{kind: kindFail, status: status, reason: "rejected"}
}

// failure is the outcome of an answer that is not a 2xx, or false for a 2xx.
func failure(resp fetchResp) (result, bool) {
	if resp.Code != "" {
		return hostResult(resp.Code), true
	}
	if resp.Status < 200 || resp.Status > 299 {
		return classify(resp.Status), true
	}
	return result{}, false
}

// jsonReq is a request with a JSON body (nil body for none).
func jsonReq(method, url string, headers map[string]string, body any) fetchReq {
	h := map[string]string{"Accept": "application/json"}
	for k, v := range headers {
		h[k] = v
	}
	r := fetchReq{Method: method, URL: url, Headers: h}
	if body != nil {
		b, _ := json.Marshal(body)
		r.Body = string(b)
		h["Content-Type"] = "application/json"
	}
	return r
}

// listName reads the name field of a provider answer.
func listName(body string) string {
	var v struct {
		Name string `json:"name"`
	}
	_ = json.Unmarshal([]byte(body), &v)
	return v.Name
}

var errNoList = result{kind: kindFail, reason: "list not found"}
