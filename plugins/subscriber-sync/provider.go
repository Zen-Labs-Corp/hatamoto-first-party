package main

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
	return nil
}

// deliver sends one job through the configured provider.
func deliver(c cfg, j job) result {
	p := providerFor(c.Provider)
	if p == nil {
		return result{kind: kindFail, reason: "not configured"}
	}
	return p.upsert(j, c, doFetch)
}
