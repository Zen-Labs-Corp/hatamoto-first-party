package main

import (
	"errors"
	"path"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

// result is the outcome of checking one link.
type result struct {
	Status     string
	HTTPStatus int
	Err        string
	// FailCount is the consecutive connection failures including this one
	// (outbound only; zero after any answer).
	FailCount int
	// Delay is the wait before the next check; zero means the owner's
	// recheck_days.
	Delay time.Duration
	// Keep means nothing could be decided: leave the stored state as it is.
	Keep bool
}

// keep is the result of a check that could not run (a host problem).
func keep() result { return result{Keep: true} }

// systemPrefixes are served by the host, not by pages.
var systemPrefixes = []string{"/media/", "/uploads/", "/p/", "/assets/"}

// maxRedirectPages bounds the redirect lookup: 25 pages of 200 rules.
const maxRedirectPages = 25

// redirectCache lazily loads the site's active redirect sources once per run.
type redirectCache struct {
	loaded bool
	active map[string]bool
}

// has reports whether an active redirect has p as its exact source path.
func (c *redirectCache) has(p string) (bool, error) {
	if !c.loaded {
		active := map[string]bool{}
		for page := 1; page <= maxRedirectPages; page++ {
			var res struct {
				Items []struct {
					SourcePath string `json:"source_path"`
					IsActive   bool   `json:"is_active"`
				} `json:"items"`
				Total int `json:"total"`
			}
			err := callHost(sdk.FnRedirectsList, map[string]any{"page": page, "per_page": 200}, &res)
			if err != nil {
				return false, err
			}
			for _, r := range res.Items {
				if r.IsActive {
					active[trimPath(r.SourcePath)] = true
				}
			}
			if len(res.Items) == 0 || page*200 >= res.Total {
				break
			}
		}
		c.active, c.loaded = active, true
	}
	return c.active[p], nil
}

// trimPath drops a query, a fragment and a trailing slash ("/" stays).
func trimPath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if p = strings.TrimRight(p, "/"); p == "" {
		return "/"
	}
	return p
}

// fileLike reports a last segment with an extension (/files/report.pdf).
func fileLike(p string) bool { return path.Ext(path.Base(p)) != "" }

func systemPath(p string) bool {
	for _, s := range systemPrefixes {
		if strings.HasPrefix(p, s) {
			return true
		}
	}
	return false
}

// checkInternal asks the host whether a published page lives at p. It never
// uses the network. A path that is no page is still fine when it is a site
// redirect, the home page, a listing above published pages, a file or a
// system path.
func checkInternal(p string, rc *redirectCache) result {
	p = trimPath(p)
	var found struct {
		ID string `json:"id"`
	}
	err := callHost(sdk.FnEntitiesGet, map[string]string{"path": p}, &found)
	if err == nil {
		return result{Status: statusOK}
	}
	var he *sdk.HostError
	if !errors.As(err, &he) || he.Code != sdk.ErrNotFound {
		logf("warn", "internal check of %s: %v", p, err)
		return keep()
	}
	redirected, err := rc.has(p)
	if err != nil {
		logf("warn", "redirect lookup: %v", err)
		return keep()
	}
	if redirected {
		return result{Status: statusRedirected}
	}
	if p == "/" || fileLike(p) || systemPath(p) {
		return result{Status: statusOK}
	}
	parent, err := isKnownParent(p)
	if err != nil {
		logf("warn", "known path lookup: %v", err)
		return keep()
	}
	if parent {
		return result{Status: statusOK}
	}
	return result{Status: statusBroken, Err: "no page"}
}
