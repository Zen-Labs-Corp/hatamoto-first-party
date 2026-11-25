// Command fp-loadcheck loads built plugin directories through the core package
// loader: the package parse, the asset read that install and serve run, then
// what the host does next with the templates: it registers the plugin's funcs,
// partials and sections and compiles them into the default theme, and a plugin
// whose templates core refuses or drops (a define outside plugin/<name>/, a
// call into another plugin, a parse error, a call cycle, scripts without
// site.scripts, a registration refusal) fails here. Only the plugin code
// itself, which needs the wasm host and a database, is not run.
// scripts/load-check.sh copies this file into core's backend/cmd/fp-loadcheck
// at the commit pinned in .hatamoto-core and runs it there, because the loader
// packages are internal to the core module.
//
// Arguments: <name>=<dir> ..., dir being a plugin's build/ directory.
// Prints one line per plugin and exits 1 if any plugin is refused.
package main

import (
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"slices"
	"strings"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
	"hatamoto.dev/core/internal/render"
	"hatamoto.dev/sdk"
)

func main() {
	bad := 0
	for _, arg := range os.Args[1:] {
		name, dir, _ := strings.Cut(arg, "=")
		label := "  " + name + " " + strings.Repeat(".", max(3, 14-len(name)))
		if err := check(label, name, dir); err != nil {
			fmt.Printf("%s FAIL %v\n", label, err)
			bad++
		}
	}
	if bad > 0 {
		os.Exit(1)
	}
}

func check(label, name, dir string) error {
	if st, err := os.Stat(dir); err != nil || !st.IsDir() {
		return fmt.Errorf("no build/ (run scripts/build-plugin.sh plugins/%s first)", name)
	}
	raw, err := packages.PackDir(dir, nil)
	if err != nil {
		return err
	}
	p, err := packages.ParseNew(raw, packages.DefaultLimits)
	if err != nil {
		return err
	}
	a, err := pluginhost.AssetsFromFS(p.FS())
	if err != nil {
		return err
	}
	man, err := p.PluginManifest()
	if err != nil {
		return err
	}
	if err := checkTemplates(man, a); err != nil {
		return err
	}
	schemas, _ := fs.Glob(p.FS(), "schemas/*.json")
	var counts []string
	for _, c := range []struct {
		what string
		n    int
	}{{"partials", len(a.Partials)}, {"sections", len(a.Sections)}, {"migrations", len(a.Migrations)}, {"schemas", len(schemas)}} {
		if c.n > 0 {
			counts = append(counts, fmt.Sprintf("%s %d", c.what, c.n))
		}
	}
	suffix := ""
	if len(counts) > 0 {
		suffix = " (" + strings.Join(counts, ", ") + ")"
	}
	fmt.Printf("%s OK%s\n", label, suffix)
	return nil
}

// checkTemplates registers the plugin's templates in core's render registry the
// way the plugin host does (registerTemplates; template funcs are stubs, as in
// the CLI) and compiles them into the default theme, which vets them.
func checkTemplates(man sdk.Manifest, a pluginhost.Assets) error {
	render.SetPluginScriptGate(func(string) bool { return slices.Contains(man.Asked(), sdk.Permission("site.scripts")) })
	render.RemovePlugin(man.Name)
	defer render.RemovePlugin(man.Name)
	var why []string
	stub := func(...any) string { return "" }
	for _, fn := range man.Funcs {
		if err := render.RegisterPluginFunc(man.Name, fn, stub); err != nil {
			why = append(why, err.Error())
		}
	}
	for name, src := range a.Partials {
		if err := render.RegisterPluginPartial(man.Name, name, src); err != nil {
			why = append(why, err.Error())
		}
	}
	for typ, src := range a.Sections {
		if err := render.RegisterPluginSection(man.Name, typ, src); err != nil {
			why = append(why, err.Error())
		}
	}
	if _, err := render.New(render.Options{Logger: slog.New(slog.NewTextHandler(io.Discard, nil))}); err != nil {
		return fmt.Errorf("templates: default theme does not compile with them: %w", err)
	}
	why = append(why, render.PluginTemplatesRefused(man.Name)...)
	why = append(why, render.PluginMarkupStopped(man.Name)...)
	if len(why) > 0 {
		return fmt.Errorf("core would refuse templates: %s", strings.Join(why, "; "))
	}
	return nil
}
