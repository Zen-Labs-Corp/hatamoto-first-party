package main

import (
	"encoding/json"
	"go/parser"
	"go/token"
	"html/template"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	"hatamoto.dev/sdk"
	"hatamoto.dev/sdk/guest"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const modulePath = "github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn"

// exportsInFile reads the //go:wasmexport names from the source text: the file
// is only compiled for wasip1, so the host build never sees it.
func exportsInFile(t *testing.T) []string {
	t.Helper()
	src, err := os.ReadFile("exports_wasip1.go")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, m := range regexp.MustCompile(`(?m)^//go:wasmexport (\S+)$`).FindAllStringSubmatch(string(src), -1) {
		names = append(names, m[1])
	}
	slices.Sort(names)
	return names
}

func expectedExports() []string {
	want := []string{
		guest.HTTPExport, sdk.RenderPage.Export(), sdk.CommentCheckF.Export(), sdk.CommentDraftF.Export(),
		sdk.CommentCreated.Export(), sdk.CommentApproved.Export(), sdk.CommentSpam.Export(),
		sdk.CommentDeleted.Export(), sdk.CommentUpdated.Export(),
	}
	for _, n := range core.CronNames {
		want = append(want, guest.CronExport(n))
	}
	slices.Sort(want)
	return want
}

func TestExportsMatchRegisteredHandlers(t *testing.T) {
	got := exportsInFile(t)
	literal := []string{
		"http_handle", "filter_render_page", "filter_comment_check", "filter_comment_draft",
		"on_comment_created", "on_comment_approved", "on_comment_spam", "on_comment_deleted", "on_comment_updated",
		"on_cron_rank", "on_cron_comment_rank", "on_cron_reconcile", "on_cron_sweep", "on_cron_search",
	}
	slices.Sort(literal)
	if !slices.Equal(got, literal) {
		t.Fatalf("exports_wasip1.go exports %v, want %v", got, literal)
	}
	if want := expectedExports(); !slices.Equal(got, want) {
		t.Fatalf("exports %v do not match the SDK export names %v", got, want)
	}

	inputs := map[string]string{
		guest.HTTPExport:                 `{"method":"GET","pattern":"/nope"}`,
		sdk.RenderPage.Export():          `{"path":"/news","blocks":[]}`,
		sdk.CommentCheckF.Export():       `{"body":"hi"}`,
		sdk.CommentDraftF.Export():       `{"commenter_key":"k","author_name":"n"}`,
		guest.CronExport("rank"):         `{"name":"rank"}`,
		guest.CronExport("comment_rank"): `{"name":"comment_rank"}`,
		guest.CronExport("reconcile"):    `{"name":"reconcile"}`,
		guest.CronExport("sweep"):        `{"name":"sweep"}`,
		guest.CronExport("search"):       `{"name":"search"}`,
	}
	for _, name := range got {
		in, ok := inputs[name]
		if !ok {
			in = `{"id":"c1"}`
		}
		out, err := guest.Handle(name, []byte(in))
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		switch name {
		case guest.HTTPExport:
			if !strings.Contains(string(out), "404") {
				t.Errorf("http_handle on an unknown route: %s", out)
			}
		case sdk.RenderPage.Export():
			var page sdk.PageContext
			if err := json.Unmarshal(out, &page); err != nil {
				t.Errorf("render.page output: %v", err)
			} else if len(page.Blocks) != 1 || page.Blocks[0].Type != "hn_userbar" || page.Blocks[0].ID != "hn_userbar" {
				t.Errorf("render.page blocks, want only the user bar: %s", out)
			}
		}
	}
}

func TestCronNamesMatchManifest(t *testing.T) {
	var manifest, exports []string
	for _, c := range loadManifest(t).Cron {
		manifest = append(manifest, c.Name)
	}
	for _, e := range exportsInFile(t) {
		if n, ok := strings.CutPrefix(e, "on_cron_"); ok {
			exports = append(exports, n)
		}
	}
	if !slices.Equal(manifest, core.CronNames) {
		t.Errorf("manifest cron %v, core.CronNames %v", manifest, core.CronNames)
	}
	slices.Sort(manifest)
	slices.Sort(exports)
	if !slices.Equal(manifest, exports) {
		t.Errorf("manifest cron %v, on_cron_* exports %v", manifest, exports)
	}
}

func TestRegisteredRoutesAreDeclaredAndOwned(t *testing.T) {
	for _, r := range registry.Routes() {
		owner, ok := routeOwners[r.Key]
		if !ok {
			t.Errorf("registered route %q is not in plugin.json", r.Key)
			continue
		}
		if owner.phase != r.Phase {
			t.Errorf("route %q registered by %s, owned by %s", r.Key, r.Phase, owner.phase)
		}
	}
}

func TestPhasesDoNotImportEachOther(t *testing.T) {
	for _, phase := range core.Phases {
		files, err := filepath.Glob(filepath.Join("internal", phase, "*.go"))
		if err != nil || len(files) == 0 {
			t.Fatalf("phase %s: no files (%v)", phase, err)
		}
		for _, f := range files {
			af, err := parser.ParseFile(token.NewFileSet(), f, nil, parser.ImportsOnly)
			if err != nil {
				t.Fatal(err)
			}
			for _, imp := range af.Imports {
				path := strings.Trim(imp.Path.Value, `"`)
				rest, ok := strings.CutPrefix(path, modulePath+"/internal/")
				if ok && rest != "core" && !strings.HasPrefix(rest, "core/") {
					t.Errorf("%s imports %s: phases talk only through internal/core", f, path)
				}
			}
		}
	}
}

func TestStylePartialDefined(t *testing.T) {
	tpl, err := template.ParseFiles("partials/style.html")
	if err != nil {
		t.Fatal(err)
	}
	if tpl.Lookup("plugin/hn/style") == nil {
		t.Error(`partials/style.html does not define "plugin/hn/style"`)
	}
}
