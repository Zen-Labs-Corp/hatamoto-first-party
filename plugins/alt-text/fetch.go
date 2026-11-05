package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"
)

const (
	pageSize      = 20 // media.list page size; offsets advance by it
	maxListCalls  = 20 // media.list calls per step
	msgNoSiteURL  = "Set the site address in the plugin settings so providers can reach your images."
	msgBadSiteURL = "Set the site address to a full https address such as https://example.com, without a query or login."
	msgListFailed = "Could not list the images in the media library."
	msgNoSettings = "Could not read the plugin settings."
	msgBadCursor  = "The run lost its place; start it again."
)

// fetchCursor is the checkpoint of the fetch phase. Off and I locate the next
// image (index I of the page at offset Off); the rest are running counts.
type fetchCursor struct {
	Off int `json:"off"`
	I   int `json:"i"`
	N   int `json:"n"` // checked: sent to the provider or failed before
	S   int `json:"s"` // suggestions kept
	D   int `json:"d"` // decorative
	E   int `json:"e"` // errors
	K   int `json:"k"` // skipped by type
}

type mediaItem struct {
	ID        string `json:"id"`
	Filename  string `json:"filename"`
	MimeType  string `json:"mime_type"`
	PublicURL string `json:"public_url"`
	AltText   string `json:"alt_text"`
}

type mediaPage struct {
	Items []mediaItem `json:"items"`
	Total int         `json:"total"`
}

// validSiteURL accepts an empty address (media links may be absolute) or an
// absolute https URL with a host and no userinfo, query or fragment.
func validSiteURL(raw string) bool {
	if raw == "" {
		return true
	}
	u, err := url.Parse(raw)
	return err == nil && u.Scheme == "https" && u.Hostname() != "" && u.User == nil &&
		u.RawQuery == "" && !u.ForceQuery && u.Fragment == "" && !strings.Contains(raw, "#")
}

// msgTick names the host the owner has to tick when starting the run.
func msgTick(host string) string { return "Tick " + host + " when you start the run." }

var errNoSiteURL = errors.New(msgNoSiteURL)

// sendable are the image types the providers accept.
var sendable = map[string]bool{"image/jpeg": true, "image/png": true, "image/gif": true, "image/webp": true}

// fetchStep makes at most one provider call, then checkpoints and returns.
// A problem with one image is counted and skipped; only a problem with the
// whole run (no key, no site address, no listing) fails the run.
func fetchStep(st sdk.RunStep) error {
	s, err := loadSettings()
	if err != nil {
		return guest.RunFail(callHost, msgNoSettings)
	}
	if s.APIKey == "" {
		return guest.RunFail(callHost, msgNoKey)
	}
	limit := int(s.Limit)
	if !validSiteURL(s.SiteURL) {
		return guest.RunFail(callHost, msgBadSiteURL)
	}
	if st.Cursor == "" {
		info, err := guest.RunInfo(callHost)
		if err != nil {
			return err
		}
		if host := providerHosts[s.Provider]; host != "" && !slices.Contains(info.Hosts, host) {
			return guest.RunFail(callHost, msgTick(host))
		}
	}
	var cur fetchCursor
	if st.Cursor != "" {
		if json.Unmarshal([]byte(st.Cursor), &cur) != nil {
			return guest.RunFail(callHost, msgBadCursor)
		}
	}

	for calls := 0; calls < maxListCalls; calls++ {
		if cur.N >= limit {
			return finishFetch(cur)
		}
		var page mediaPage
		err := callHost("media.list", map[string]any{"mime_type": "image/", "limit": pageSize, "offset": cur.Off}, &page)
		if err != nil {
			return guest.RunFail(callHost, msgListFailed)
		}
		if cur.Off >= page.Total {
			return finishFetch(cur)
		}
		for ; cur.I < len(page.Items) && cur.N < limit; cur.I++ {
			it := page.Items[cur.I]
			if !sendable[strings.ToLower(it.MimeType)] {
				cur.K++
				continue
			}
			if s.onlyMissing() && strings.TrimSpace(it.AltText) != "" {
				continue
			}
			sent, err := suggest(s, it, &cur)
			if errors.Is(err, errNoSiteURL) {
				return guest.RunFail(callHost, msgNoSiteURL)
			}
			var re *runError
			if errors.As(err, &re) {
				return guest.RunFail(callHost, re.Reason)
			}
			if !sent {
				continue
			}
			cur.I++
			return checkpointFetch(cur, len(page.Items), limit)
		}
		cur.Off += pageSize
		cur.I = 0
	}
	return checkpointFetch(cur, -1, limit)
}

// suggest handles one image that qualifies and updates the counts. sent is
// true when a provider call was made (the step ends after one).
func suggest(s settings, it mediaItem, cur *fetchCursor) (sent bool, err error) {
	imageURL, err := absURL(it.PublicURL, s.SiteURL)
	if err != nil {
		return false, err
	}
	cur.N++
	if !strings.HasPrefix(imageURL, "https://") {
		cur.E++
		return false, nil
	}
	answer, err := describe(s, imageURL, strings.ToLower(it.MimeType))
	var re *runError
	if errors.As(err, &re) {
		return false, err
	}
	if err != nil {
		cur.E++
		return true, nil
	}
	if isDecorative(answer) {
		if guest.BufferPut(callHost, "d/"+it.ID, []byte(it.Filename)) != nil {
			cur.E++
		} else {
			cur.D++
		}
		return true, nil
	}
	alt := cleanAlt(answer, s.MaxLength)
	if alt == "" {
		cur.E++
		return true, nil
	}
	val, _ := json.Marshal(struct {
		Alt string `json:"alt"`
	}{alt})
	if guest.BufferPut(callHost, "s/"+it.ID, val) != nil {
		cur.E++
	} else {
		cur.S++
	}
	return true, nil
}

// absURL makes a media public_url something a provider can fetch: an absolute
// URL (a CDN) is kept, a /media path gets the site address in front.
func absURL(publicURL, siteURL string) (string, error) {
	u := strings.TrimSpace(publicURL)
	if u == "" {
		return "", nil
	}
	low := strings.ToLower(u)
	if strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") {
		return u, nil
	}
	if siteURL == "" {
		return "", errNoSiteURL
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + u
	}
	return siteURL + u, nil
}

// checkpointFetch saves the cursor after an image. pageLen is the length of
// the page the cursor is on (-1: none); at its end the cursor moves on.
func checkpointFetch(cur fetchCursor, pageLen, limit int) error {
	if pageLen >= 0 && cur.I >= pageLen {
		cur.Off += pageSize
		cur.I = 0
	}
	raw, _ := json.Marshal(cur)
	if err := guest.RunCheckpoint(callHost, string(raw)); err != nil {
		return err
	}
	_ = guest.RunProgress(callHost, min(cur.N, limit), limit, fmt.Sprintf("Checked %d of %d images", min(cur.N, limit), limit))
	return nil
}

// meta is what the transform phase needs to report.
type meta struct {
	Checked    int `json:"checked"`
	Suggested  int `json:"suggested"`
	Decorative int `json:"decorative"`
	Errors     int `json:"errors"`
	Skipped    int `json:"skipped"`
}

func finishFetch(cur fetchCursor) error {
	raw, _ := json.Marshal(meta{Checked: cur.N, Suggested: cur.S, Decorative: cur.D, Errors: cur.E, Skipped: cur.K})
	_ = guest.BufferPut(callHost, "meta", raw)
	return guest.RunFinish(callHost, nil)
}
