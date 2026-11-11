package main

import (
	"regexp"
	"strconv"
	"strings"
	"time"

	"hatamoto.dev/sdk"
)

const (
	termsPath  = "/p/devportal/terms"
	dateLayout = "2 Jan 2006"
)

// flashMessages are the texts behind the one-shot flash codes.
var flashMessages = map[string]string{
	"state":      "That sign-in link expired or did not start here. Please try again.",
	"denied":     "GitHub sign-in was cancelled.",
	"noemail":    "Your GitHub account has no verified email address. Verify one on GitHub and try again.",
	"suspended":  "This developer account is suspended.",
	"failed":     "Something went wrong. Please try again in a moment.",
	"name":       "Enter a display name of up to 80 characters.",
	"email":      "Pick one of your verified GitHub emails.",
	"terms":      "Accept the Developer terms to create your account.",
	"key":        "That is not a valid public key. Paste the base64 public key, not the private one.",
	"privatekey": "That is a private key. Do not share it: treat it as exposed, generate a new key pair and register the public key (the .pub file).",
	"keytaken":   "That key is already registered to another account.",
	"haskey":     "This account already has a key.",
}

// pageFilter fills the portal section of the /developers page. Every other
// path returns at once, before any settings read or host call: the hook runs
// on every public page view of the site.
func pageFilter(p sdk.PageContext) (sdk.PageContext, error) {
	if p.Path != portalPath {
		return p, nil
	}
	if len(p.Blocks) == 0 {
		return p, nil
	}
	idx := 0
	for i, b := range p.Blocks {
		if b.Fields["anchor"] == "devportal" {
			idx = i
			break
		}
	}
	st, err := loadSettings()
	if err != nil {
		return p, nil // fail open: the placeholder block stays
	}
	sess, err := lookupSession(p.Cookies["sid"])
	if err != nil {
		return p, nil
	}

	c := map[string]any{"terms_url": termsPath, "flash": ""}
	var cookies []sdk.SetCookie
	var resultID int64
	if code := p.Cookies["fl"]; code != "" {
		c["flash"] = flashText(code)
		resultID = submissionFlashID(code)
		cookies = append(cookies, clearCookie("fl"))
	}
	if p.Cookies["sid"] != "" && sess == nil {
		cookies = append(cookies, clearCookie("sid"))
	}

	if sess != nil && sess.Slid {
		// The browser drops the cookie after its own Max-Age: renew it with the row.
		cookies = append(cookies, sdk.SetCookie{Name: "sid", Value: p.Cookies["sid"], MaxAge: int(sessionTTL / time.Second)})
	}

	switch {
	case sess == nil:
		if st.ClientID == "" || st.PublicURL == "" {
			c["state"] = "notice"
			c["flash"] = "Developer sign-in is not available yet."
			break
		}
		state := p.Cookies["st"]
		if state == "" {
			state = newToken()
			cookies = append(cookies, sdk.SetCookie{Name: "st", Value: state, MaxAge: stateTTL})
		}
		c["state"] = "signed_out"
		c["authorize_url"] = authorizeURL(st, state)
	case sess.AccountID == nil:
		c["state"] = "signup"
		c["csrf"] = sess.CSRF
		c["login"] = sess.GH.Login
		setEmails(c, sess.GH.Emails)
	case sess.Status != "active":
		c["state"] = "suspended"
		c["login"] = sess.Login
	default:
		c["state"] = "dashboard"
		c["csrf"] = sess.CSRF
		c["login"] = sess.Login
		c["display_name"] = sess.DisplayName
		c["plan"] = planLabel(sess.Plan)
		c["has_key"] = sess.KeyID != ""
		c["key_id"] = sess.KeyID
		if sess.KeyID != "" && !sess.KeyAdded.IsZero() {
			c["key_added"] = sess.KeyAdded.UTC().Format(dateLayout)
		}
		c["can_submit"] = sess.KeyID != ""
		if err := addPackages(c, *sess.AccountID, resultID); err != nil {
			return p, nil // fail open: the placeholder block stays
		}
	}

	b := p.Blocks[idx]
	p.Blocks[idx] = sdk.Block{ID: b.ID, Type: "devportal", Title: b.Title, Content: c}
	p.SetCookies = append(p.SetCookies, cookies...)
	return p, nil
}

var downloadFlashStatus = regexp.MustCompile(`^sub-download-([0-9]{3})$`)

var tooLargeFlashBytes = regexp.MustCompile(`^sub-toolarge-([0-9]{1,15})$`)

var submissionFlash = regexp.MustCompile(`^s([0-9]{1,18})$`)

// flashText is the message behind a flash code: the portal codes and the
// submit codes. A submission code ("s12") has no text of its own.
func flashText(code string) string {
	if m, ok := flashMessages[code]; ok {
		return m
	}
	if m := downloadFlashStatus.FindStringSubmatch(code); m != nil {
		return "We could not download that file: download answered " + m[1] + ". Check the release URL."
	}
	if m := tooLargeFlashBytes.FindStringSubmatch(code); m != nil {
		return "That file is too large: over " + m[1] + " bytes."
	}
	return submitFlash[code]
}

// submissionFlashID is the submission id in a flash code such as "s12", 0 for
// any other code.
func submissionFlashID(code string) int64 {
	m := submissionFlash.FindStringSubmatch(code)
	if m == nil {
		return 0
	}
	id, _ := strconv.ParseInt(m[1], 10, 64)
	return id
}

// packageRow is the latest submission of one package plus its listing.
type packageRow struct {
	Name          string `json:"name"`
	Kind          string `json:"kind"`
	Version       string `json:"version"`
	Status        string `json:"status"`
	Reason        string `json:"reason"`
	URL           string `json:"url"`
	Source        string `json:"source"`
	Title         string `json:"title"`
	About         string `json:"about"`
	Category      string `json:"category"`
	SiteType      string `json:"site_type"`
	Screenshot    string `json:"screenshot"`
	ListedVersion string `json:"listed_version"`
	ListingStatus string `json:"listing_status"`
}

// packagesDoc is what the one dashboard query returns.
type packagesDoc struct {
	Packages []packageRow `json:"packages"`
	Result   *struct {
		Name    string  `json:"name"`
		Version string  `json:"version"`
		Status  string  `json:"status"`
		Checks  []check `json:"checks"`
	} `json:"result"`
}

const maxPackageRows = 20

// addPackages reads the developer's packages and, when resultID is one of the
// account's submissions, its result, with ONE read-only query.
func addPackages(c map[string]any, account, resultID int64) error {
	var doc packagesDoc
	err := dbJSON(`SELECT jsonb_build_object('packages', coalesce((SELECT jsonb_agg(to_jsonb(p) ORDER BY p.name, p.kind) FROM (`+
		`SELECT s.name, s.kind, s.version, s.status, s.reason, s.url, s.source, s.title, s.about, s.category, s.site_type, s.screenshot, `+
		`l.version AS listed_version, l.status AS listing_status `+
		`FROM (SELECT DISTINCT ON (kind, name) name, kind, version, status, reason, url, source, title, about, category, site_type, screenshot `+
		`FROM submissions WHERE account = $1 ORDER BY kind, name, id DESC) s `+
		`LEFT JOIN listings l ON l.kind = s.kind AND l.name = s.name AND l.account = $1 `+
		`ORDER BY s.name, s.kind LIMIT `+strconv.Itoa(maxPackageRows)+`) p), '[]'::jsonb), `+
		`'result', (SELECT to_jsonb(r) FROM (SELECT name, version, status, checks FROM submissions WHERE id = $2 AND account = $1) r))`,
		&doc, account, resultID)
	if err != nil {
		return err
	}
	rows := make([]any, 0, len(doc.Packages))
	for _, r := range doc.Packages {
		rows = append(rows, r.content())
	}
	c["packages"] = rows
	c["categories"] = stringList(Categories)
	c["site_types"] = stringList(SiteTypes)
	if r := doc.Result; r != nil {
		lines := make([]any, 0, len(r.Checks))
		for _, k := range r.Checks {
			lines = append(lines, map[string]any{"ok": k.OK, "text": k.Text})
		}
		title := r.Name + " " + r.Version
		if r.Name == "" {
			title = "Your package"
		}
		c["result"] = map[string]any{"title": title + ", " + statusLabel(r.Status), "checks": lines}
	}
	return nil
}

func stringList(l []string) []any {
	out := make([]any, len(l))
	for i, s := range l {
		out[i] = s
	}
	return out
}

func statusLabel(status string) string {
	switch status {
	case "waiting":
		return "Waiting for approval"
	case "refused":
		return "Not submitted"
	case "rejected":
		return "Rejected"
	case "listed":
		return "Listed"
	case "superseded":
		return "Replaced by a newer version"
	case "revoked":
		return "Revoked"
	}
	return status
}

func listingLabel(status string) string {
	switch status {
	case "active":
		return "Listed"
	case "suspended":
		return "Suspended"
	case "revoked":
		return "Revoked"
	case "dropped":
		return "Removed, the file changed"
	}
	return status
}

// content turns a row into the section's list item: the listed version, the
// version in review, and the hidden fields of the Resubmit form.
func (r packageRow) content() map[string]any {
	m := map[string]any{"name": r.Name, "kind": "Plugin"}
	if r.Kind == "theme" {
		m["kind"] = "Theme"
	}
	if r.ListedVersion != "" {
		m["listed"] = r.ListedVersion + " " + listingLabel(r.ListingStatus)
	}
	switch r.Status {
	case "waiting", "rejected", "refused":
		m["latest"] = r.Version + " " + statusLabel(r.Status)
		if r.Version == "" {
			m["latest"] = statusLabel(r.Status)
		}
		if r.Status == "rejected" {
			m["reason"] = r.Reason
		}
		m["can_resubmit"] = r.Status != "waiting"
	default:
		if r.ListedVersion == "" {
			m["latest"] = statusLabel(r.Status)
		}
	}
	m["form_kind"], m["form_url"], m["form_source"], m["form_title"] = r.Kind, r.URL, r.Source, r.Title
	m["form_about"], m["form_category"], m["form_site_type"], m["form_screenshot"] = r.About, r.Category, r.SiteType, r.Screenshot
	return m
}

// setEmails puts the emails in as a list and, as a fallback for a host that
// drops nested content, as the flat keys email1..email5.
func setEmails(c map[string]any, emails []string) {
	list := make([]any, 0, len(emails))
	for i, e := range emails {
		list = append(list, e)
		if i < 5 {
			c["email"+strconv.Itoa(i+1)] = e
		}
	}
	c["emails"] = list
}

func planLabel(plan string) string {
	if plan == "" {
		return ""
	}
	return strings.ToUpper(plan[:1]) + plan[1:]
}
