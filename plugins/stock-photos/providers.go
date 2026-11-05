package main

import (
	"encoding/base64"
	"encoding/json"
	"net/url"
	"path"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// Page sizes are constants so that the same page is requested at every step of
// a run (the cursor is a raw result index).
const (
	pexelsPageSize    = 30
	openversePageSize = 20
)

// searchTimeoutMS keeps the search well inside the 10 s step cut, so that a
// slow search still leaves room for imports (see importStartLimit).
const (
	searchTimeoutMS = 3000
	maxImportBytes  = 8 << 20 // a larger known original would not download in time
)

// Core limits alt, caption and credit in bytes (1024, 2000 and 500).
const (
	maxAlt         = 500 // runes, then cut to maxAltBytes
	maxAltBytes    = 1024
	maxCreditBytes = 500
	maxCaption     = 200 // runes: at most 800 bytes, under core's 2000
	maxName        = 120
	maxCreditWho   = 120 // bytes of the author name kept in a credit
	maxCreditURL   = 200 // longer source URLs are left out of the credit
)

type fetchReq struct {
	Method    string            `json:"method,omitempty"`
	URL       string            `json:"url"`
	Headers   map[string]string `json:"headers,omitempty"`
	TimeoutMS int               `json:"timeout_ms,omitempty"`
}

type fetchResp struct {
	Status    int    `json:"status"`
	Body      string `json:"body"`
	BodyB64   string `json:"body_b64"`
	Truncated bool   `json:"truncated"`
}

// photo is one raw search result. Results are returned aligned with the
// provider's list (the run cursor counts raw positions); Skip marks the ones
// that must not be imported.
type photo struct {
	Provider, ID, ImageURL, Filename, Alt, Credit, Caption string
	Skip                                                   bool
	SkipReason                                             string
}

type searchParams struct {
	Query, Provider, Orientation string
	Page                         int
}

// providerError carries a user-facing message and never any request detail.
type providerError struct{ Kind, Message string }

func (e *providerError) Error() string { return e.Message }

const (
	msgRate = "The provider is rate limiting requests. Try again later."
	msgAuth = "Pexels rejected the API key. Check it in the plugin settings."
	msgKey  = "Add your Pexels API key in the plugin settings, or switch to Openverse."
)

// getJSON performs a GET and decodes a 2xx JSON body into out. authMsg is the
// message for a 401/403 (empty: a generic refusal).
func getJSON(rawURL string, headers map[string]string, authMsg string, out any) error {
	var resp fetchResp
	req := fetchReq{Method: "GET", URL: rawURL, Headers: headers, TimeoutMS: searchTimeoutMS}
	if err := callHost("http.fetch", req, &resp); err != nil {
		return err
	}
	switch {
	case resp.Status == 401 || resp.Status == 403:
		if authMsg == "" {
			authMsg = "The provider refused the request (HTTP " + strconv.Itoa(resp.Status) + ")."
		}
		return &providerError{"auth", authMsg}
	case resp.Status == 429:
		return &providerError{"rate", msgRate}
	case resp.Status < 200 || resp.Status > 299:
		return &providerError{"status", "The provider answered with HTTP " + strconv.Itoa(resp.Status) + "."}
	case resp.Truncated:
		return &providerError{"truncated", "The provider response was too large."}
	}
	body := []byte(resp.Body)
	if resp.Body == "" && resp.BodyB64 != "" {
		b, err := base64.StdEncoding.DecodeString(resp.BodyB64)
		if err != nil {
			return &providerError{"decode", "The provider response could not be read."}
		}
		body = b
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &providerError{"decode", "The provider response could not be read."}
	}
	return nil
}

func searchPexels(key string, p searchParams) (photos []photo, more bool, rawCount int, err error) {
	if key == "" {
		return nil, false, 0, &providerError{"key", msgKey}
	}
	q := "query=" + url.QueryEscape(p.Query) + "&per_page=" + strconv.Itoa(pexelsPageSize) + "&page=" + strconv.Itoa(p.Page)
	if p.Orientation != "" && p.Orientation != "any" {
		q += "&orientation=" + url.QueryEscape(p.Orientation)
	}
	var out struct {
		NextPage string `json:"next_page"`
		Photos   []struct {
			ID           int64  `json:"id"`
			URL          string `json:"url"`
			Photographer string `json:"photographer"`
			Alt          string `json:"alt"`
			Src          struct {
				Large2x string `json:"large2x"`
				Large   string `json:"large"`
			} `json:"src"`
		} `json:"photos"`
	}
	if err := getJSON("https://api.pexels.com/v1/search?"+q, map[string]string{"Authorization": key}, msgAuth, &out); err != nil {
		return nil, false, 0, err
	}
	for _, r := range out.Photos {
		img := r.Src.Large2x
		if img == "" {
			img = r.Src.Large
		}
		if r.ID == 0 || !isHTTP(img) {
			photos = append(photos, photo{Skip: true, SkipReason: "incomplete result"})
			continue
		}
		id := strconv.FormatInt(r.ID, 10)
		who := cutBytes(cleanText(r.Photographer, 100), maxCreditWho)
		if who == "" {
			who = "unknown photographer"
		}
		alt := cleanAlt(r.Alt)
		if alt == "" {
			alt = cleanAlt(p.Query)
		}
		credit := "Photo by " + who + " on Pexels (Pexels License)"
		if isCreditURL(r.URL) {
			credit += " " + r.URL
		}
		photos = append(photos, photo{
			Provider: "pexels", ID: id, ImageURL: img,
			Filename: filenameFor("pexels", id, img, ""),
			Alt:      alt,
			Credit:   cutBytes(cleanText(credit, maxCreditBytes), maxCreditBytes),
			Caption:  cleanText(who+" / Pexels", maxCaption),
		})
	}
	return photos, out.NextPage != "", len(out.Photos), nil
}

var imageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true, "gif": true}

func searchOpenverse(p searchParams) (photos []photo, more bool, rawCount int, err error) {
	// Commas in license_type and source are sent literally: the API answers 500
	// to %2C. The sources are the ones whose image hosts the manifest declares.
	q := "q=" + url.QueryEscape(p.Query) + "&page_size=" + strconv.Itoa(openversePageSize) + "&page=" + strconv.Itoa(p.Page) +
		"&license_type=commercial,modification&source=flickr,wikimedia"
	switch p.Orientation {
	case "landscape":
		q += "&aspect_ratio=wide"
	case "portrait":
		q += "&aspect_ratio=tall"
	case "square":
		q += "&aspect_ratio=square"
	}
	var out struct {
		Page      int `json:"page"`
		PageCount int `json:"page_count"`
		Results   []struct {
			ID                string `json:"id"`
			Title             string `json:"title"`
			Creator           string `json:"creator"`
			URL               string `json:"url"`
			ForeignLandingURL string `json:"foreign_landing_url"`
			License           string `json:"license"`
			LicenseVersion    string `json:"license_version"`
			Source            string `json:"source"`
			Provider          string `json:"provider"`
			Filetype          string `json:"filetype"`
			Filesize          int64  `json:"filesize"`
		} `json:"results"`
	}
	if err := getJSON("https://api.openverse.org/v1/images/?"+q, nil, "", &out); err != nil {
		return nil, false, 0, err
	}
	for _, r := range out.Results {
		lic := strings.ToLower(strings.TrimSpace(r.License))
		ext := extOf(r.Filetype, r.URL)
		switch {
		case r.ID == "" || !isHTTP(r.URL):
			photos = append(photos, photo{Skip: true, SkipReason: "incomplete result"})
			continue
		case !allowedLicences[lic]:
			photos = append(photos, photo{Skip: true, SkipReason: "licence not allowed: " + lic})
			continue
		case r.Filesize > maxImportBytes:
			photos = append(photos, photo{Skip: true, SkipReason: "image too large"})
			continue
		case !imageExts[ext]:
			photos = append(photos, photo{Skip: true, SkipReason: "not a supported image type"})
			continue
		}
		title := cleanText(r.Title, 200)
		alt := cleanAlt(title)
		if alt == "" {
			alt = cleanAlt(p.Query)
			title = "Untitled"
		}
		who := cutBytes(cleanText(r.Creator, 100), maxCreditWho)
		if who == "" {
			who = "unknown author"
		}
		src := cleanText(r.Source, 50)
		if src == "" {
			src = cleanText(r.Provider, 50)
		}
		if src == "" {
			src = "Openverse"
		}
		// The licence and the source URL are never cut: only the title gives way.
		tail := licenceLabel(lic, r.LicenseVersion)
		if isCreditURL(r.ForeignLandingURL) {
			tail += ", " + r.ForeignLandingURL
		}
		suffix := " by " + who + ", " + tail
		credit := cutBytes(title, maxCreditBytes-len(suffix)) + suffix
		photos = append(photos, photo{
			Provider: "openverse", ID: r.ID, ImageURL: r.URL,
			Filename: filenameFor("openverse", r.ID, r.URL, r.Filetype),
			Alt:      alt,
			Credit:   cutBytes(cleanText(credit, maxCreditBytes), maxCreditBytes),
			Caption:  cleanText(who+" / "+src, maxCaption),
		})
	}
	return photos, out.Page < out.PageCount, len(out.Results), nil
}

// isCreditURL reports whether a source URL may go into a credit: plain http(s),
// no credentials, short enough to keep whole.
func isCreditURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && isHTTP(s) && u.User == nil && len(s) <= maxCreditURL
}

// allowedLicences are the Openverse licence codes that may be staged: they
// allow commercial use and modification.
var allowedLicences = map[string]bool{"cc0": true, "pdm": true, "by": true, "by-sa": true}

func licenceLabel(lic, version string) string {
	switch lic {
	case "cc0":
		return "CC0 1.0"
	case "pdm":
		return "Public Domain Mark 1.0"
	}
	label := "CC " + strings.ToUpper(lic)
	if v := strings.TrimSpace(version); v != "" {
		label += " " + v
	}
	return label
}

func isHTTP(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// extOf returns the lowercase extension from the declared file type or, failing
// that, the URL path; "" when neither names one.
func extOf(filetype, rawURL string) string {
	if t := strings.ToLower(strings.TrimPrefix(strings.TrimSpace(filetype), ".")); t != "" {
		return t
	}
	if u, err := url.Parse(rawURL); err == nil {
		return strings.ToLower(strings.TrimPrefix(path.Ext(u.Path), "."))
	}
	return ""
}

// filenameFor builds "<provider>-<id>.<ext>" restricted to [a-z0-9._-].
func filenameFor(provider, id, rawURL, filetype string) string {
	ext := extOf(filetype, rawURL)
	if !imageExts[ext] {
		ext = "jpg"
	}
	base := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '.', r == '_', r == '-':
			return r
		}
		return '-'
	}, strings.ToLower(provider+"-"+id))
	if limit := maxName - len(ext) - 1; len(base) > limit {
		base = base[:limit]
	}
	return base + "." + ext
}

// cleanText drops control and invisible format characters (bidi overrides,
// zero-width), collapses whitespace and cuts to max
// runes.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) || unicode.In(r, unicode.Cf, unicode.Co, unicode.Cs) || r == utf8.RuneError {
			return -1
		}
		return r
	}, s)
	s = strings.Join(strings.Fields(s), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return strings.TrimSpace(s)
}

// cleanAlt cleans alt text to core's limit (bytes, not runes).
func cleanAlt(s string) string {
	return strings.TrimSpace(cutBytes(cleanText(s, maxAlt), maxAltBytes))
}

// cutBytes cuts s to at most n bytes on a rune boundary.
func cutBytes(s string, n int) string {
	if n < 0 {
		n = 0
	}
	if len(s) <= n {
		return s
	}
	for n > 0 && !utf8.RuneStart(s[n]) {
		n--
	}
	return s[:n]
}
