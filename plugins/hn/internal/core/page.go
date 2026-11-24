package core

import "hatamoto.dev/sdk"

// Page is the render.page value a handler edits, with the viewer and block helpers.
type Page struct {
	Ctx        *sdk.PageContext
	viewer     *User
	viewerDone bool
}

// Query is the value of a declared page_query key, or "".
func (p *Page) Query(k string) string { return p.Ctx.Query[k] }

// Viewer is the member behind the page's commenter key, or nil (signed out, no
// username yet, or a host error: fail open).
func (p *Page) Viewer() *User {
	if !p.viewerDone {
		p.viewerDone = true
		if u, err := UserByKey(p.Ctx.CommenterKey); err == nil {
			p.viewer = u
		}
	}
	return p.viewer
}

// Find returns the index of the first block of that type, or -1.
func (p *Page) Find(blockType string) int {
	for i, b := range p.Ctx.Blocks {
		if b.Type == blockType {
			return i
		}
	}
	return -1
}

// Prepend puts a block first.
func (p *Page) Prepend(b sdk.Block) {
	p.Ctx.Blocks = append([]sdk.Block{b}, p.Ctx.Blocks...)
}

// Append puts a block last.
func (p *Page) Append(b sdk.Block) { p.Ctx.Blocks = append(p.Ctx.Blocks, b) }

// Replace swaps the block at index i; an index out of range appends instead.
func (p *Page) Replace(i int, b sdk.Block) {
	if i < 0 || i >= len(p.Ctx.Blocks) {
		p.Append(b)
		return
	}
	p.Ctx.Blocks[i] = b
}
