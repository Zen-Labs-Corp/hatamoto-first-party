package main

import (
	"net/url"
	"slices"
	"strings"
	"time"
)

// job is one queued submission to deliver.
type job struct {
	ID            int64             `json:"id"`
	SubmissionID  string            `json:"submission_id"`
	FormSlug      string            `json:"form_slug"`
	Provider      string            `json:"provider"`
	Email         string            `json:"email"`
	Fields        map[string]string `json:"fields"`
	SourcePath    string            `json:"source_path"`
	SubmittedAt   time.Time         `json:"submitted_at"`
	Attempts      int               `json:"attempts"`
	NextAttemptAt time.Time         `json:"next_attempt_at"`
}

// mapRow maps one submitted form field to one CRM field.
type mapRow struct {
	FormField string `json:"form_field"`
	CRMField  string `json:"crm_field"`
}

// crmField is one CRM field offered to the mapping table.
type crmField struct {
	Name     string `json:"name"`
	Label    string `json:"label"`
	Required bool   `json:"required"`
}

// provider talks to one CRM. Every upsert is safe to repeat: it looks the
// record up by email before it writes.
type provider interface {
	// upsert writes the record, then the note; it never duplicates a record.
	upsert(j job, c cfg, m []mapRow, f fetcher) result
	// test checks the credentials and names the account.
	test(c cfg, f fetcher) (account string, r result)
	// fields lists the CRM fields a form field can be mapped to.
	fields(c cfg, f fetcher) ([]crmField, result)
	// refresh renews the access token (Salesforce); the others only report
	// kindUpdated.
	refresh(c cfg, f fetcher) result
}

// providers is filled by each provider_<name>.go in init().
var providers = map[string]provider{}

// providerFor is nil for an unknown provider name.
func providerFor(name string) provider { return providers[name] }

// deliverFn is the seam tests replace.
var deliverFn = deliver

// deliver sends one job through the configured provider.
func deliver(c cfg, j job, m []mapRow) result {
	p := providerFor(c.Provider)
	if p == nil {
		return result{kind: kindFail, reason: "not configured"}
	}
	return p.upsert(j, c, m, doFetch)
}

// retryWithoutRejected sends build(skip). When the answer is a 400 or 207 and
// names(resp) returns CRM field names that were in sent and are not skipped
// yet, it drops exactly those and sends ONCE more. It returns the final answer
// and the dropped names, sorted. When no usable name is parsed the first answer
// is returned untouched.
func retryWithoutRejected(build func(skip map[string]bool) fetchReq, names func(fetchResp) []string, sent []string, f fetcher) (fetchResp, []string) {
	first := f(build(nil))
	if first.Code != "" || (first.Status != 400 && first.Status != 207) {
		return first, nil
	}
	skip := map[string]bool{}
	var dropped []string
	for _, n := range names(first) {
		if slices.Contains(sent, n) && !skip[n] {
			skip[n] = true
			dropped = append(dropped, n)
		}
	}
	if len(dropped) == 0 {
		return first, nil
	}
	slices.Sort(dropped)
	return f(build(skip)), dropped
}

// rejectedMessage is the log message for dropped fields, or "".
func rejectedMessage(dropped []string) string {
	if len(dropped) == 0 {
		return ""
	}
	return "rejected fields: " + strings.Join(dropped, ", ")
}

// recordURLOK reports whether raw is an https URL whose host is one of the
// suffixes or a subdomain of one. Record links come from the provider's
// answers or settings and are shown as links, so they are checked first.
func recordURLOK(raw string, suffixes ...string) bool {
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.User != nil || u.Port() != "" {
		return false
	}
	h := strings.ToLower(u.Hostname())
	for _, s := range suffixes {
		if h == s || strings.HasSuffix(h, "."+s) {
			return true
		}
	}
	return false
}
