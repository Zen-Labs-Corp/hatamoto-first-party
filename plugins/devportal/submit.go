package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"hatamoto.dev/sdk"
)

const (
	maxAbout            = 600
	maxTitle            = 80
	maxURL              = 2048
	submitsPerHour      = 10
	inspectBusyFlash    = "sub-busy"
	signCommand         = "hatamoto plugin pack . --key acme.key"
	submissionFlashPref = "s"
)

// submitFlash holds the messages of the submit route. The page merges them
// into its flash lookup.
var submitFlash = map[string]string{
	"sub-url":        "Use the link to the .hmp file of a GitHub release, like https://github.com/acme/acme-seo/releases/download/v1.2.0/acme-seo-1.2.0.hmp.",
	"sub-source":     "The source link must be a GitHub repository, like https://github.com/acme/acme-seo.",
	"sub-kind":       "Choose plugin or theme.",
	"sub-about":      "The description must be between 1 and 600 characters.",
	"sub-title":      "The title can be at most 80 characters.",
	"sub-category":   "Choose a category for a plugin, or a site type for a theme.",
	"sub-screenshot": "A screenshot link must be an https link and is for themes only.",
	"sub-nokey":      "Add your public key before you submit.",
	"sub-limit":      "You sent 10 submissions in the last hour. Try again later.",
	inspectBusyFlash: "Could not check the package right now. Try again in a minute.",
}

// submitForm is the submit form after trimming.
type submitForm struct {
	Kind, URL, Source, Title, About, Category, SiteType, Screenshot string
}

var (
	pathPart = regexp.MustCompile(`^[A-Za-z0-9._+~-]+$`)
	ghName   = regexp.MustCompile(`^[A-Za-z0-9._-]+$`)
)

func okName(s string) bool { return ghName.MatchString(s) && s != "." && s != ".." }

// releaseURLOK accepts https://github.com/<owner>/<repo>/releases/download/<tag>/<file>.hmp.
func releaseURLOK(raw string) bool {
	u, ok := githubURL(raw)
	if !ok || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	p := strings.Split(strings.TrimPrefix(u.Path, "/"), "/")
	if len(p) != 6 || !okName(p[0]) || !okName(p[1]) || p[2] != "releases" || p[3] != "download" {
		return false
	}
	return pathPart.MatchString(p[4]) && pathPart.MatchString(p[5]) && strings.HasSuffix(p[5], ".hmp") && p[5] != ".hmp" && p[4] != ".." && p[4] != "."
}

// sourceURLOK accepts https://github.com/<owner>/<repo> with an optional slash.
func sourceURLOK(raw string) bool {
	u, ok := githubURL(raw)
	if !ok || u.RawQuery != "" || u.Fragment != "" {
		return false
	}
	p := strings.Split(strings.TrimSuffix(strings.TrimPrefix(u.Path, "/"), "/"), "/")
	return len(p) == 2 && okName(p[0]) && okName(p[1])
}

func githubURL(raw string) (*url.URL, bool) {
	if len(raw) > maxURL || strings.ContainsAny(raw, " \t\r\n\\") {
		return nil, false
	}
	u, err := url.Parse(raw)
	if err != nil || u.Scheme != "https" || u.Host != "github.com" || u.User != nil || u.Opaque != "" {
		return nil, false
	}
	return u, true
}

// compareVersions compares two semantic versions: the numeric core first, then
// a pre-release sorts below the release. Build metadata is ignored. Anything
// unparseable compares as 0.0.0.
func compareVersions(a, b string) int {
	ac, ap := splitVersion(a)
	bc, bp := splitVersion(b)
	for i := range ac {
		if ac[i] != bc[i] {
			if ac[i] < bc[i] {
				return -1
			}
			return 1
		}
	}
	switch {
	case ap == "" && bp == "":
		return 0
	case ap == "":
		return 1
	case bp == "":
		return -1
	}
	x, y := strings.Split(ap, "."), strings.Split(bp, ".")
	for i := 0; i < len(x) && i < len(y); i++ {
		if c := comparePre(x[i], y[i]); c != 0 {
			return c
		}
	}
	switch {
	case len(x) < len(y):
		return -1
	case len(x) > len(y):
		return 1
	}
	return 0
}

func comparePre(a, b string) int {
	an, aerr := strconv.Atoi(a)
	bn, berr := strconv.Atoi(b)
	switch {
	case aerr == nil && berr == nil:
		if an != bn {
			if an < bn {
				return -1
			}
			return 1
		}
		return 0
	case aerr == nil:
		return -1
	case berr == nil:
		return 1
	}
	return strings.Compare(a, b)
}

func splitVersion(v string) (core [3]int, pre string) {
	v = strings.TrimPrefix(strings.TrimSpace(v), "v")
	if i := strings.IndexByte(v, '+'); i >= 0 {
		v = v[:i]
	}
	if i := strings.IndexByte(v, '-'); i >= 0 {
		v, pre = v[:i], v[i+1:]
	}
	for i, part := range strings.SplitN(v, ".", 3) {
		core[i], _ = strconv.Atoi(part)
	}
	return core, pre
}

func jsonList(l []string) string {
	if l == nil {
		l = []string{}
	}
	b, _ := json.Marshal(l)
	return string(b)
}

func jsonChecks(c []check) string {
	if c == nil {
		c = []check{}
	}
	b, _ := json.Marshal(c)
	return string(b)
}

// readForm trims and validates the fields; a non-empty code is the flash code
// of the first refusal.
func readForm(v url.Values) (f submitForm, code string) {
	get := func(k string) string { return strings.TrimSpace(v.Get(k)) }
	f = submitForm{Kind: get("kind"), URL: get("url"), Source: get("source"), Title: get("title"), About: get("about"),
		Category: get("category"), SiteType: get("site_type"), Screenshot: get("screenshot")}
	switch {
	case f.Kind != "plugin" && f.Kind != "theme":
		return f, "sub-kind"
	case !releaseURLOK(f.URL):
		return f, "sub-url"
	case f.Source != "" && !sourceURLOK(f.Source):
		return f, "sub-source"
	case utf8.RuneCountInString(f.Title) > maxTitle || strings.IndexFunc(f.Title, unicode.IsControl) >= 0:
		return f, "sub-title"
	case f.About == "" || utf8.RuneCountInString(f.About) > maxAbout:
		return f, "sub-about"
	}
	if f.Kind == "plugin" {
		f.SiteType = ""
		if !contains(Categories, f.Category) {
			return f, "sub-category"
		}
		if f.Screenshot != "" {
			return f, "sub-screenshot"
		}
		return f, ""
	}
	f.Category = ""
	if !contains(SiteTypes, f.SiteType) {
		return f, "sub-category"
	}
	if f.Screenshot != "" {
		u, err := url.Parse(f.Screenshot)
		if err != nil || u.Scheme != "https" || u.Host == "" || len(f.Screenshot) > maxURL || strings.ContainsAny(f.Screenshot, " \t\r\n") {
			return f, "sub-screenshot"
		}
	}
	return f, ""
}

// errBusy means the inspection did not complete: any host error (timeout,
// blocked, rate limited, unavailable, denied).
var errBusy = errors.New("could not check")

// runChecks inspects the package and builds the submission row with its
// checks. errBusy means the inspection itself did not complete.
func runChecks(acct int64, pub, keyID string, f submitForm) (submission, error) {
	var res sdk.InspectResult
	if err := callHost("packages.inspect", sdk.InspectRequest{URL: f.URL, SignerKey: pub}, &res); err != nil {
		var he *sdk.HostError
		if errors.As(err, &he) {
			return submission{}, errBusy
		}
		return submission{}, err
	}
	s := submission{Account: acct, Kind: f.Kind, Name: res.Name, Version: res.Version, URL: f.URL, Source: f.Source, Title: f.Title,
		About: f.About, Category: f.Category, SiteType: f.SiteType, Screenshot: f.Screenshot, SHA256: res.SHA256, Size: res.Size,
		Permissions: res.Permissions, AllowedHosts: res.AllowedHosts, Description: res.Description, SignerKey: pub}
	add := func(ok bool, format string, a ...any) {
		s.Checks = append(s.Checks, check{OK: ok, Text: fmt.Sprintf(format, a...)})
	}

	switch {
	case res.SignatureOK && res.SignerKeyID == keyID:
		add(true, "Signed by your key")
	case res.SignerKeyID == "":
		add(false, "The package is not signed. Sign it with: %s", signCommand)
	default:
		add(false, "Signed by %s, not by your key %s. Sign it with: %s", res.SignerKeyID, keyID, signCommand)
	}
	if res.Kind == f.Kind {
		add(true, "It is a %s", f.Kind)
	} else {
		add(false, "You chose %s, but the package is a %s", f.Kind, res.Kind)
	}
	addNameChecks(&s, res.Name, acct, add)
	if s.Version == "" {
		add(false, "The package has no version")
	} else if listed, err := listedVersion(f.Kind, res.Name); err != nil {
		return submission{}, errBusy
	} else if listed != "" && compareVersions(res.Version, listed) <= 0 {
		add(false, "Version %s is not newer than the listed %s", res.Version, listed)
	} else {
		add(true, "Version %s", res.Version)
	}
	for _, p := range res.Problems {
		add(false, "%s", p)
	}
	if revoked, err := keyRevoked(pub); err != nil {
		return submission{}, errBusy
	} else if revoked {
		add(false, "Your key was revoked")
	}
	add(true, "Size %.1f MB", float64(res.Size)/(1<<20))
	if len(res.Permissions) > 0 {
		add(true, "Asks for: %s", strings.Join(res.Permissions, ", "))
	}
	return s, nil
}

// addNameChecks adds the checks on the package name: shape, reserved, owner.
func addNameChecks(s *submission, name string, acct int64, add func(bool, string, ...any)) {
	switch nameRefusal(name) {
	case "invalid":
		add(false, "%q is not a valid name (lowercase letters, digits and dashes, 2 to 40 characters)", name)
		return
	case "reserved":
		add(false, "The name %s is reserved", name)
		return
	}
	owner, err := nameOwner(name)
	switch {
	case err != nil:
		add(false, "Could not check the name, try again")
	case !ownsOrFree(owner, acct):
		add(false, "The name %s belongs to another developer", name)
	default:
		add(true, "The name %s is yours to use", name)
	}
}

func allOK(c []check) bool {
	for _, x := range c {
		if !x.OK {
			return false
		}
	}
	return true
}

// handleSubmit checks a release and stores the result as a submission. A
// package that fails a check is stored as refused so the developer sees why.
func handleSubmit(r sdk.HTTPRequest) sdk.HTTPResponse {
	return handleForm(r, func(pf postForm) sdk.HTTPResponse {
		s := pf.sess
		if s.AccountID == nil {
			return back("")
		}
		if s.Status != "active" {
			return back("suspended")
		}
		if s.KeyID == "" {
			return back("sub-nokey")
		}
		f, code := readForm(pf.form)
		if code != "" {
			return back(code)
		}
		n, err := recentSubmissions(*s.AccountID, now().Add(-time.Hour))
		if err != nil {
			return back(inspectBusyFlash)
		}
		if n >= submitsPerHour {
			return back("sub-limit")
		}
		pub, keyID, err := accountPubkey(*s.AccountID)
		if err != nil || pub == "" {
			return back(inspectBusyFlash)
		}
		sub, err := runChecks(*s.AccountID, pub, keyID, f)
		if err != nil {
			if !errors.Is(err, errBusy) {
				logf("warn", "submit check failed")
			}
			return back(inspectBusyFlash)
		}
		sub.Status = "waiting"
		if !allOK(sub.Checks) {
			sub.Status = "refused"
		}
		id, err := insertSubmission(sub)
		if errors.Is(err, errInactiveAccount) {
			return back("suspended")
		}
		if err != nil {
			logf("warn", "submission store failed")
			return back("failed")
		}
		if sub.Status == "waiting" {
			notifyReviewer(s.Login, sub)
		}
		return back(submissionFlashPref + strconv.FormatInt(id, 10))
	})
}

// notifyReviewer queues the new-submission mail to review_email when set.
func notifyReviewer(login string, s submission) {
	st, err := loadSettings()
	if err != nil || st.ReviewEmail == "" {
		return
	}
	notify(st.ReviewEmail, fmt.Sprintf("New %s submission: %s %s", s.Kind, s.Name, s.Version),
		fmt.Sprintf("%s submitted %s %s (%s) for review. Open the admin page of the developer portal to approve or reject it.\n", login, s.Name, s.Version, s.Kind))
}
