// Command fp-loadcheck loads built plugin directories through the core package
// loader: the package parse, then the asset read that install and serve run.
// scripts/load-check.sh copies this file into core's backend/cmd/fp-loadcheck
// at the commit pinned in .hatamoto-core and runs it there, because the loader
// packages are internal to the core module.
//
// Arguments: <name>=<dir> ..., dir being a plugin's build/ directory.
// Prints one line per plugin and exits 1 if any plugin is refused.
package main

import (
	"fmt"
	"io/fs"
	"os"
	"strings"

	"hatamoto.dev/core/internal/packages"
	"hatamoto.dev/core/internal/pluginhost"
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
