package main

import (
	"encoding/json"
	"fmt"
	"math"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	// certCache is how long a lookup, good or failed, is kept before the host
	// is asked again.
	certCache = 12 * time.Hour
	// crtLayout is how crt.sh writes not_after: UTC, without a zone.
	crtLayout = "2006-01-02T15:04:05"

	lookupUnknown = "unknown"
)

// runCertsTick is the "certs" cron job. It never returns an error.
func runCertsTick(sdk.CronEvent) error {
	certsTick()
	return nil
}

// httpsHosts returns the distinct lower-case host names of the https monitors.
// Addresses given as an IP are left out: no certificate log lists them.
func httpsHosts(monitors []monitor) []string {
	seen := map[string]bool{}
	var hosts []string
	for _, m := range monitors {
		u, err := url.Parse(m.URL)
		if err != nil || u.Scheme != "https" {
			continue
		}
		h := strings.ToLower(u.Hostname())
		if h == "" || seen[h] || net.ParseIP(h) != nil {
			continue
		}
		seen[h] = true
		hosts = append(hosts, h)
	}
	return hosts
}

func certsTick() {
	start := now()
	deadline := start.Add(tickBudget)
	monitors, err := listMonitors()
	if err != nil {
		logf("warn", "uptime-monitor: monitors unavailable code=%s", hostErrCode(err))
		return
	}
	st := loadSettings()

	var stale []cert
	for _, h := range httpsHosts(monitors) {
		c, found, err := getCert(h)
		if err != nil {
			logf("warn", "uptime-monitor: certificate state unavailable code=%s", hostErrCode(err))
			return
		}
		if !found {
			c = cert{Host: h}
		}
		if c.LookedUpAt == nil || start.Sub(*c.LookedUpAt) >= certCache {
			stale = append(stale, c)
		}
	}
	// Round robin: the host looked up longest ago, or never, goes first.
	sort.SliceStable(stale, func(i, j int) bool {
		a, b := stale[i].LookedUpAt, stale[j].LookedUpAt
		if a == nil || b == nil {
			return a == nil && b != nil
		}
		return a.Before(*b)
	})

	for _, c := range stale {
		timeout := min(maxRequestMS, int(deadline.Sub(now())/time.Millisecond)-int(tickMargin/time.Millisecond))
		if timeout < minRequestMS {
			break
		}
		if err := refreshCert(c, timeout, st); err != nil {
			logf("warn", "uptime-monitor: certificate not recorded host=%s code=%s", c.Host, hostErrCode(err))
			break
		}
	}
	if err := flushOutbox(deadline); err != nil {
		logf("warn", "uptime-monitor: mail queue failed code=%s", hostErrCode(err))
	}
}

// refreshCert looks the host up and stores the outcome. A failed lookup keeps
// the earlier expiry and is retried after the cache time.
func refreshCert(c cert, timeoutMS int, st settings) error {
	at := now()
	c.LookedUpAt = &at
	notAfter, issuer, err := lookupCert(c.Host, timeoutMS)
	if err != nil {
		code := hostErrCode(err)
		if code == "" {
			code = lookupUnknown
		}
		logf("warn", "uptime-monitor: certificate lookup failed host=%s code=%s", c.Host, code)
		c.LookupError = lookupUnknown
		return upsertCert(c)
	}
	c.LookupError = ""
	if c.NotAfter != nil && notAfter.After(*c.NotAfter) {
		c.Warned = nil // renewed: the thresholds count again
	}
	c.NotAfter, c.Issuer = &notAfter, issuer
	if err := queueExpiryWarning(&c, st, at); err != nil {
		return err
	}
	return upsertCert(c)
}

// crtRow is one certificate in the crt.sh JSON answer.
type crtRow struct {
	IssuerName string `json:"issuer_name"`
	NameValue  string `json:"name_value"`
	NotAfter   string `json:"not_after"`
}

// errNoCert is a lookup that found nothing usable.
var errNoCert = hostErr(sdk.ErrInternal, "no matching certificate")

// lookupCert asks crt.sh for the certificates of host and returns the expiry
// and issuer of the newest one that covers it.
func lookupCert(host string, timeoutMS int) (time.Time, string, error) {
	res, err := fetch(fetchReq{
		Method:    http.MethodGet,
		URL:       "https://crt.sh/?q=" + url.QueryEscape(host) + "&output=json&exclude=expired",
		TimeoutMS: timeoutMS,
	})
	if err != nil {
		return time.Time{}, "", err
	}
	// A cut-off body is never parsed: it would be a half list.
	if res.Status != http.StatusOK || res.Truncated {
		return time.Time{}, "", errNoCert
	}
	var rows []crtRow
	if err := json.Unmarshal([]byte(res.Body), &rows); err != nil {
		return time.Time{}, "", errNoCert
	}
	var best time.Time
	issuer := ""
	for _, r := range rows {
		t, err := time.ParseInLocation(crtLayout, r.NotAfter, time.UTC)
		if err != nil || !covers(r.NameValue, host) {
			continue
		}
		if t.After(best) {
			best, issuer = t, strings.TrimSpace(r.IssuerName)
		}
	}
	if best.IsZero() {
		return time.Time{}, "", errNoCert
	}
	return best, issuer, nil
}

// covers reports whether a certificate with the \n-separated names covers
// host: by an exact name or by a wildcard for its parent domain.
func covers(nameValue, host string) bool {
	host = strings.ToLower(host)
	wild := ""
	if _, parent, ok := strings.Cut(host, "."); ok {
		wild = "*." + parent
	}
	for _, n := range strings.Split(nameValue, "\n") {
		n = strings.ToLower(strings.TrimSpace(n))
		if n == host || (wild != "" && n == wild) {
			return true
		}
	}
	return false
}

// queueExpiryWarning queues one mail for the smallest warning threshold that
// has been reached and not yet mailed, and marks every threshold at or above
// the days left as warned. Nothing is queued, and nothing marked, without
// alert emails.
func queueExpiryWarning(c *cert, st settings, at time.Time) error {
	if len(st.AlertEmails) == 0 || c.NotAfter == nil {
		return nil
	}
	left := int(math.Floor(c.NotAfter.Sub(at).Hours() / 24))
	days := slices.Clone(st.SSLWarnDays)
	slices.Sort(days)
	unmet := false
	for _, d := range days {
		if left <= d && !slices.Contains(c.Warned, d) {
			unmet = true
			break
		}
	}
	if !unmet {
		return nil
	}
	left = max(left, 0)
	unit := "days"
	if left == 1 {
		unit = "day"
	}
	body := fmt.Sprintf("The certificate of %s expires in %d %s.\n\nExpires: %s\nIssuer: %s\nSource: crt.sh\n",
		c.Host, left, unit, c.NotAfter.UTC().Format("2006-01-02"), c.Issuer)
	subject := mailSubject("[SSL] ", c.Host, fmt.Sprintf(" certificate expires in %d %s", left, unit))
	if err := enqueueMail(subject, body, st.AlertEmails); err != nil {
		return err
	}
	for _, d := range days {
		if d >= left && !slices.Contains(c.Warned, d) {
			c.Warned = append(c.Warned, d)
		}
	}
	return nil
}
