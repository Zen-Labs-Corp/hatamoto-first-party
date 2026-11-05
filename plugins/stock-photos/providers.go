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

const (
	maxAlt     = 500
	maxCredit  = 500
	maxCaption = 200
	maxName    = 120
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
	req := fetchReq{Method: "GET", URL: rawURL, Headers: headers, TimeoutMS: 8000}
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
		who := cleanText(r.Photographer, 100)
		if who == "" {
			who = "unknown photographer"
		}
		alt := cleanText(r.Alt, maxAlt)
		if alt == "" {
			alt = cleanText(p.Query, maxAlt)
		}
		credit := "Photo by " + who + " on Pexels (Pexels License)"
		if isHTTP(r.URL) {
			credit += " " + r.URL
		}
		photos = append(photos, photo{
			Provider: "pexels", ID: id, ImageURL: img,
			Filename: filenameFor("pexels", id, img, ""),
			Alt:      alt,
			Credit:   cleanText(credit, maxCredit),
			Caption:  cleanText(who+" / Pexels", maxCaption),
		})
	}
	return photos, out.NextPage != "", len(out.Photos), nil
}

var imageExts = map[string]bool{"jpg": true, "jpeg": true, "png": true, "webp": true, "gif": true}

func searchOpenverse(p searchParams) (photos []photo, more bool, rawCount int, err error) {
	// The comma in license_type is sent literally: the API answers 500 to %2C.
	q := "q=" + url.QueryEscape(p.Query) + "&page_size=" + strconv.Itoa(openversePageSize) + "&page=" + strconv.Itoa(p.Page) +
		"&license_type=commercial,modification"
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
		case lic == "" || hasToken(lic, "nc") || hasToken(lic, "nd"):
			photos = append(photos, photo{Skip: true, SkipReason: "licence not allowed: " + lic})
			continue
		case !imageExts[ext]:
			photos = append(photos, photo{Skip: true, SkipReason: "not a supported image type"})
			continue
		}
		title := cleanText(r.Title, 200)
		alt := title
		if alt == "" {
			alt = cleanText(p.Query, maxAlt)
			title = "Untitled"
		}
		who := cleanText(r.Creator, 100)
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
		credit := title + " by " + who + ", " + licenceLabel(lic, r.LicenseVersion)
		if isHTTP(r.ForeignLandingURL) {
			credit += ", " + r.ForeignLandingURL
		}
		photos = append(photos, photo{
			Provider: "openverse", ID: r.ID, ImageURL: r.URL,
			Filename: filenameFor("openverse", r.ID, r.URL, r.Filetype),
			Alt:      alt,
			Credit:   cleanText(credit, maxCredit),
			Caption:  cleanText(who+" / "+src, maxCaption),
		})
	}
	return photos, out.Page < out.PageCount, len(out.Results), nil
}

func hasToken(lic, tok string) bool {
	for _, t := range strings.Split(lic, "-") {
		if t == tok {
			return true
		}
	}
	return false
}

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

// cleanText drops control characters, collapses whitespace and cuts to max
// runes.
func cleanText(s string, max int) string {
	s = strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		if unicode.IsControl(r) || r == utf8.RuneError {
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
