package main

import (
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

// permText describes one permission for a human reader: the wording of core's
// install prompt and directory, so a card here reads like the one core builds.
type permText struct {
	Help  string // full sentence shown next to the permission name
	Short string // badge label of a risky permission; empty otherwise
	Warn  bool   // the permission deserves a visible warning
}

var permTexts = map[string]permText{
	sdk.PermEntitiesRead:  {Help: "Read pages, posts and other content"},
	sdk.PermTermsRead:     {Help: "Read categories and tags"},
	sdk.PermMenusRead:     {Help: "Read menus"},
	sdk.PermMediaRead:     {Help: "Read the media library"},
	sdk.PermRedirectsRead: {Help: "Read redirects"},
	sdk.PermFormsRead:     {Help: "Hear about form submissions"},
	sdk.PermCommentsRead:  {Help: "Read approved comments, hear about new, approved, spam and deleted comments, add to the spam score of new comments and see a pseudonymous key for each commenter"},
	sdk.PermClientKey: {
		Help:  "Sees a pseudonymous key for each visitor's address and network on its pages and routes (not the address itself). The key is stable, so the plugin can recognise a returning visitor and block a network. It is personal data: your privacy notice should say so.",
		Short: "Pseudonymous visitor keys.", Warn: true,
	},
	sdk.PermEntitiesWriteOwn: {
		Help:  "Creates, edits and deletes entries of ONE content type it declares, and publishes them at once, without your review. Rate limited and logged in the audit log. It cannot touch any other content.",
		Short: "Creates and edits content in its own content type.", Warn: true,
	},
	sdk.PermCommentsModerate: {
		Help:  "Marks comments on its own content type dead or restores them and sets their order, without your review. It cannot post comments or touch other content types. Logged in the audit log.",
		Short: "Moderates comments on its own content type.", Warn: true,
	},
	sdk.PermCommentsIdentity: {
		Help:  "Signs visitors in as commenters with accounts this plugin manages (no email). Those commenters can then post comments. Logged in the audit log.",
		Short: "Sign visitors in as commenters.", Warn: true,
	},
	sdk.PermSettings:  {Help: "Store its own settings"},
	sdk.PermLog:       {Help: "Write to the server log"},
	sdk.PermMailSend:  {Help: "Send email"},
	sdk.PermHTTPFetch: {Help: "Call other web addresses it lists"},
	sdk.PermHTTPFetchAny: {
		Help:  "WARNING: send requests to ANY public web address, including addresses typed into its settings. Treat it as a way out to the internet from this server.",
		Short: "Any web address.", Warn: true,
	},
	sdk.PermPackagesInspect: {Help: "Download and check Hatamoto packages from the web"},
	sdk.PermKV:              {Help: "Keep small values of its own"},
	sdk.PermDB:              {Help: "Keep its own data tables"},
	sdk.PermBuilderData:     {Help: "Offer data sources to the page builder"},
	sdk.PermEntitiesFilter: {
		Help:  "Sees every page, post and draft when it is saved, and can change or refuse the save.",
		Short: "Review and change content as you save it.", Warn: true,
	},
	sdk.PermUICamera: {Help: "Use the camera on this plugin's admin page (for example to scan tickets)"},
	sdk.PermSiteScripts: {
		Help:  "Runs its JavaScript on every public page, with full access to the site, including signed-in admins. Without it the plugin's script is not loaded.",
		Short: "JavaScript on every public page.", Warn: true,
	},
	sdk.PermBuilderIsland: {
		Help:  "WARNING: runs JavaScript on your site's pages, with the same access as the pages themselves. Without it the widgets still render, but their script is not served.",
		Short: "Runs JavaScript on your site's pages.", Warn: true,
	},
}

// lookupPerm returns the wording for a permission. A name this build does not
// know (a registry can be newer than the plugin) falls back to the raw name.
func lookupPerm(name string) permText {
	if t, ok := permTexts[name]; ok {
		return t
	}
	return permText{Help: name}
}

// permLine is one permission of a card in plain English.
type permLine struct {
	Name string `json:"name"`
	Text string `json:"text"`
	Warn bool   `json:"warn"`
}

// permCard maps raw permission names to the card view: the count, the distinct
// Short labels of risky permissions in order, and one line per permission. The
// warning icon already signals the risk, so the "WARNING: " prefix is dropped.
func permCard(perms []string) (count int, warn []string, lines []permLine) {
	for _, p := range perms {
		t := lookupPerm(p)
		if t.Warn && !slices.Contains(warn, t.Short) {
			warn = append(warn, t.Short)
		}
		lines = append(lines, permLine{Name: p, Text: strings.TrimPrefix(t.Help, "WARNING: "), Warn: t.Warn})
	}
	return len(perms), warn, lines
}
