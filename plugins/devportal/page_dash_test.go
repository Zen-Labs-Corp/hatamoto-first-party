package main

import (
	"html/template"
	"os"
	"strings"
	"testing"
)

const packagesDocJSON = `{"packages":[
 {"name":"acme-seo","kind":"plugin","version":"1.3.0","status":"waiting","reason":"","url":"https://github.com/acme/acme-seo/releases/download/v1.3.0/a.hmp","listed_version":"1.2.0","listing_status":"active"},
 {"name":"acme-theme","kind":"theme","version":"0.4.0","status":"rejected","reason":"Needs a screenshot","url":"https://github.com/acme/t/releases/download/v0.4.0/t.hmp","source":"https://github.com/acme/t","title":"Acme \"Theme\"","about":"A theme","site_type":"blog","listed_version":null,"listing_status":null}
],"result":null}`

func dashboard(t *testing.T, h *portal, cookies map[string]string) map[string]any {
	t.Helper()
	h.login("tok", accountRow("c1", "active", "3f9a1c2b7d4e5f60"))
	cookies["sid"] = "tok"
	return content(t, mustFilter(t, devPage(cookies)))
}

func TestDashboardListsPackagesWithStatusAndReason(t *testing.T) {
	h := newPortal(t)
	h.packages = packagesDocJSON
	c := dashboard(t, h, map[string]string{})
	rows, _ := c["packages"].([]any)
	if len(rows) != 2 || c["can_submit"] != true {
		t.Fatalf("%v", c)
	}
	a, b := rows[0].(map[string]any), rows[1].(map[string]any)
	if a["name"] != "acme-seo" || a["kind"] != "Plugin" || a["listed"] != "1.2.0 Listed" || a["latest"] != "1.3.0 Waiting for approval" || a["can_resubmit"] != false {
		t.Errorf("row 1 %v", a)
	}
	if b["kind"] != "Theme" || b["latest"] != "0.4.0 Rejected" || b["reason"] != "Needs a screenshot" || b["can_resubmit"] != true || b["form_site_type"] != "blog" {
		t.Errorf("row 2 %v", b)
	}
	if c["result"] != nil {
		t.Errorf("result without a flash: %v", c["result"])
	}
}

func TestDashboardQueryIsOneReadOnlyStatementScopedToTheAccount(t *testing.T) {
	h := newPortal(t)
	dashboard(t, h, map[string]string{"fl": "s12"})
	var n int
	for _, call := range h.calls {
		if strings.Contains(call.sql, "'packages'") {
			n++
			if len(call.args) != 2 || call.args[0] != int64(7) || call.args[1] != int64(12) || !sqlGuardOK(call.sql) || call.fn != "db.query" {
				t.Errorf("query %+v", call)
			}
		}
	}
	if n != 1 {
		t.Fatalf("%d package queries", n)
	}
	for _, call := range h.calls {
		if call.fn == "db.exec" && !strings.Contains(call.sql, "sessions") {
			t.Errorf("page view wrote: %s", call.sql)
		}
	}
}

func TestResultShowsChecksAndFlashClearsAfterView(t *testing.T) {
	h := newPortal(t)
	h.packages = `{"packages":[],"result":{"name":"acme-seo","version":"1.2.0","status":"waiting","checks":[{"ok":true,"text":"Signed by your key"},{"ok":false,"text":"Size is odd"}]}}`
	h.login("tok", accountRow("c1", "active", "3f9a1c2b7d4e5f60"))
	out := mustFilter(t, devPage(map[string]string{"sid": "tok", "fl": "s12"}))
	c := content(t, out)
	r, _ := c["result"].(map[string]any)
	if r["title"] != "acme-seo 1.2.0, Waiting for approval" {
		t.Fatalf("%v", r)
	}
	if lines, _ := r["checks"].([]any); len(lines) != 2 || lines[1].(map[string]any)["ok"] != false {
		t.Fatalf("%v", r["checks"])
	}
	if c["flash"] != "" {
		t.Errorf("a submission code is not a flash text: %v", c["flash"])
	}
	cleared := false
	for _, sc := range out.SetCookies {
		cleared = cleared || (sc.Name == "fl" && sc.MaxAge == 1)
	}
	if !cleared {
		t.Errorf("fl not cleared: %+v", out.SetCookies)
	}
}

func TestResultOfAnotherAccountIsNotShown(t *testing.T) {
	h := newPortal(t)
	h.packages = `{"packages":[],"result":null}` // the query matched no row for this account
	c := dashboard(t, h, map[string]string{"fl": "s99"})
	if c["result"] != nil {
		t.Fatalf("%v", c["result"])
	}
	for _, call := range h.calls {
		if strings.Contains(call.sql, "'packages'") && !strings.Contains(call.sql, "account = $1") {
			t.Error("result query is not scoped to the session account")
		}
	}
}

func TestSubmitFlashCodesAndPortalCodesBothResolve(t *testing.T) {
	h := newPortal(t)
	for code, want := range map[string]string{"sub-url": submitFlash["sub-url"], "denied": flashMessages["denied"], "sub-busy": submitFlash["sub-busy"]} {
		c := dashboard(t, h, map[string]string{"fl": code})
		if c["flash"] != want || want == "" {
			t.Errorf("%s: %v", code, c["flash"])
		}
	}
}

func TestDashboardFailsOpenWhenThePackageQueryFails(t *testing.T) {
	h := newPortal(t)
	h.login("tok", accountRow("c1", "active", ""))
	inner := callHost
	callHost = func(fn string, args, out any) error {
		if m, _ := args.(map[string]any); m != nil && strings.Contains(m["sql"].(string), "'packages'") {
			return errDB
		}
		return inner(fn, args, out)
	}
	in := devPage(map[string]string{"sid": "tok"})
	if out := mustFilter(t, in); out.Blocks[0].Type != "text" {
		t.Fatalf("page changed: %+v", out.Blocks[0])
	}
}

func TestSectionRendersListResultAndForms(t *testing.T) {
	raw, err := os.ReadFile("sections/devportal.html")
	if err != nil {
		t.Fatal(err)
	}
	tpl := template.Must(template.New("x").Parse(string(raw)))
	h := newPortal(t)
	h.packages = `{"packages":` + strings.TrimSuffix(strings.TrimPrefix(packagesDocJSON, `{"packages":`), `,"result":null}`) +
		`,"result":{"name":"acme-seo","version":"1.3.0","status":"refused","checks":[{"ok":true,"text":"Signed by your key"},{"ok":false,"text":"<b>x</b>"}]}}`
	c := dashboard(t, h, map[string]string{"fl": "s1"})
	var sb strings.Builder
	if err := tpl.ExecuteTemplate(&sb, "section-devportal", map[string]any{"Content": c}); err != nil {
		t.Fatal(err)
	}
	out := sb.String()
	for _, want := range []string{"acme-seo", "1.2.0 Listed", "1.3.0 Waiting for approval", `"Needs a screenshot"`, "Resubmit", "Check and submit",
		"acme-seo 1.3.0, Not submitted", "✓ Signed by your key", "✗ &lt;b&gt;x&lt;/b&gt;", `value="Acme &#34;Theme&#34;"`, `<option value="SEO">`, `<option value="blog">`} {
		if !strings.Contains(out, want) {
			t.Errorf("output lacks %q", want)
		}
	}
	if strings.Contains(out, "<b>x</b>") {
		t.Error("check text was not escaped")
	}
	if n := strings.Count(out, "Resubmit"); n != 1 {
		t.Errorf("%d Resubmit buttons, want 1 (the waiting row has none)", n)
	}
}

func TestSectionWithoutAKeyShowsNoSubmitForm(t *testing.T) {
	raw, _ := os.ReadFile("sections/devportal.html")
	tpl := template.Must(template.New("x").Parse(string(raw)))
	var sb strings.Builder
	c := map[string]any{"state": "dashboard", "csrf": "c", "login": "acme", "has_key": false, "can_submit": false}
	if err := tpl.ExecuteTemplate(&sb, "section-devportal", map[string]any{"Content": c}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(sb.String(), "Check and submit") || !strings.Contains(sb.String(), "Available once a key is added") {
		t.Error(sb.String())
	}
	for _, bad := range []string{"—", "–"} {
		if strings.Contains(string(raw), bad) {
			t.Errorf("section has a dash %q", bad)
		}
	}
}
