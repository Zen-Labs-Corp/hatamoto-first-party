package main

import (
	"regexp"

	"hatamoto.dev/sdk"
)

const (
	entitySchema = "registry-entry"
	dirType      = "registry_directory"
	detailType   = "registry_detail"
	dirAnchor    = "registry-directory"
	detailAnchor = "registry-detail"
)

// detailPathRe is the shape of a directory detail path (core's registry route).
var detailPathRe = regexp.MustCompile(`^/(plugins|themes)/([a-z][a-z0-9-]{1,39})$`)

// pageFilter fills the directory pages. Every other path returns at once,
// before any host call: the hook runs on every public page view of the site.
// It never fails a render: any problem leaves the page as it was.
func pageFilter(p sdk.PageContext) (out sdk.PageContext, err error) {
	defer func() {
		if r := recover(); r != nil {
			out, err = p, nil
		}
	}()
	switch {
	case p.Path == "/plugins" || p.Path == "/themes":
		return fillDirectory(p), nil
	case detailPathRe.MatchString(p.Path):
		return fillDetail(p), nil
	}
	return p, nil
}

// fillDirectory replaces the placeholder block of /plugins or /themes with the
// directory of that kind. A page without a placeholder is left alone.
func fillDirectory(p sdk.PageContext) sdk.PageContext {
	idx := -1
	for i, b := range p.Blocks {
		if b.Fields["anchor"] == dirAnchor || b.Type == dirType {
			idx = i
			break
		}
	}
	if idx < 0 {
		return p
	}
	s := views()
	content := map[string]any{"linked": true}
	if p.Path == "/plugins" {
		content["state"], content["cards"], content["list_url"] = s.Plugins.State, s.Plugins.Cards, "/plugins"
	} else {
		content["state"], content["cards"], content["list_url"] = s.Themes.State, s.Themes.Cards, "/themes"
	}
	b := p.Blocks[idx]
	blocks := append([]sdk.Block(nil), p.Blocks...)
	blocks[idx] = sdk.Block{ID: b.ID, Type: dirType, Title: b.Title, Content: content}
	p.Blocks = blocks
	return p
}

// fillDetail puts the registry_detail block on the page of a registry-entry
// entity when the live directory lists that name as that kind. Unknown names,
// the other kind and entities of other content types are left alone.
func fillDetail(p sdk.PageContext) sdk.PageContext {
	if p.Entity == nil || p.Entity.SchemaSlug != entitySchema {
		return p
	}
	m := detailPathRe.FindStringSubmatch(p.Path)
	kind, name := m[1], m[2]
	s := views()
	content := map[string]any{}
	switch kind {
	case "plugins":
		card, ok := findPlugin(s.Plugins, name)
		if !ok {
			return p
		}
		content["type"], content["list_name"], content["list_url"], content["plugin"] = "plugin", "Plugins", "/plugins", card
	default:
		card, ok := findTheme(s.Themes, name)
		if !ok {
			return p
		}
		content["type"], content["list_name"], content["list_url"], content["theme"] = "theme", "Themes", "/themes", card
	}
	blocks := append([]sdk.Block(nil), p.Blocks...)
	for i, b := range blocks {
		if b.Type == detailType || b.Fields["anchor"] == detailAnchor {
			blocks[i] = sdk.Block{ID: b.ID, Type: detailType, Title: b.Title, Content: content}
			p.Blocks = blocks
			return p
		}
	}
	p.Blocks = append(blocks, sdk.Block{ID: detailAnchor, Type: detailType, Content: content})
	return p
}

// findPlugin and findTheme look a name up in a verified directory only.
func findPlugin(d pluginDir, name string) (pluginCard, bool) {
	if d.State == stateOK {
		for _, c := range d.Cards {
			if c.Name == name {
				return c, true
			}
		}
	}
	return pluginCard{}, false
}

func findTheme(d themeDir, name string) (themeCard, bool) {
	if d.State == stateOK {
		for _, c := range d.Cards {
			if c.Name == name {
				return c, true
			}
		}
	}
	return themeCard{}, false
}
