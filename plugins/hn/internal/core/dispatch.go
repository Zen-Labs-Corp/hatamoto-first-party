package core

import (
	"errors"
	"slices"
	"strings"

	"hatamoto.dev/sdk"
)

var (
	errGateFailed  = errors.New("Something went wrong, try again.")
	errDraftFailed = errors.New("core: draft handler failed")
)

// ServeHTTP is the one http_handle handler. An unknown route answers 404; a
// handler error or panic answers 500 without the error text.
func (r *Registry) ServeHTTP(req sdk.HTTPRequest) (resp sdk.HTTPResponse, err error) {
	e, ok := r.routes[req.Method+" "+req.Pattern]
	if !ok {
		return JSONError(404, "not found"), nil
	}
	defer func() {
		if recover() != nil {
			resp, err = JSONError(500, "internal"), nil
		}
	}()
	resp, herr := e.h(NewRequest(req))
	if herr != nil {
		return JSONError(500, "internal"), nil
	}
	return resp, nil
}

func (pe pageEntry) matches(path string) bool {
	switch {
	case pe.all:
		return true
	case pe.prefix != "":
		return strings.HasPrefix(path, pe.prefix) && len(path) > len(pe.prefix)
	}
	return path == pe.path
}

// RenderPage is the one render.page filter. Matching handlers run in phase
// order; one that errors or panics is undone (blocks, hidden ids and cookies
// as they were before it) and the rest still run. It never returns an error.
func (r *Registry) RenderPage(ctx sdk.PageContext) (sdk.PageContext, error) {
	p := &Page{Ctx: &ctx}
	for _, pe := range r.pages {
		if !pe.matches(ctx.Path) {
			continue
		}
		blocks, hide, cookies := slices.Clone(ctx.Blocks), slices.Clone(ctx.Hide), slices.Clone(ctx.SetCookies)
		if err := runPage(pe.h, p); err != nil {
			ctx.Blocks, ctx.Hide, ctx.SetCookies = blocks, hide, cookies
		}
	}
	return ctx, nil
}

func runPage(h func(*Page) error, p *Page) (err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("core: page handler panicked")
		}
	}()
	return h(p)
}

// CheckComment is the one comment.check filter. The host holds the comment on
// any error, so it NEVER returns one: a handler that errors or panics is
// skipped, an answer that lowers the score is ignored, reasons are appended.
func (r *Registry) CheckComment(c sdk.CommentCheck) (sdk.CommentCheck, error) {
	cur := c
	for _, h := range r.checks {
		out, err := runCheck(h, cur)
		if err != nil || out.Score < cur.Score {
			continue
		}
		cur.Score, cur.Reasons = out.Score, out.Reasons
	}
	return cur, nil
}

func runCheck(h func(sdk.CommentCheck) (sdk.CommentCheck, error), c sdk.CommentCheck) (out sdk.CommentCheck, err error) {
	defer func() {
		if recover() != nil {
			err = errors.New("core: check handler panicked")
		}
	}()
	return h(c)
}

// DraftComment is the one comment.draft filter, the only hook that rejects on
// purpose. Handlers run in phase order: a Reject (or an error carrying the SDK
// reject prefix) wins at once; the first non-empty AuthorName is returned when
// nobody rejects; any other error is returned, so the host rejects the draft
// (fail closed). With no handler the input is returned unchanged.
func (r *Registry) DraftComment(d sdk.CommentDraft) (sdk.CommentDraft, error) {
	var named *sdk.CommentDraft
	for _, h := range r.drafts {
		out, err := runDraft(h, d)
		if err != nil {
			if msg, ok := strings.CutPrefix(err.Error(), sdk.RejectPrefix); ok {
				d.Reject = msg
				return d, nil
			}
			return d, err
		}
		if out.Reject != "" {
			d.Reject = out.Reject
			return d, nil
		}
		if named == nil && out.AuthorName != "" {
			named = &out
		}
	}
	if named != nil {
		return *named, nil
	}
	return d, nil
}

func runDraft(h func(sdk.CommentDraft) (sdk.CommentDraft, error), d sdk.CommentDraft) (out sdk.CommentDraft, err error) {
	defer func() {
		if recover() != nil {
			err = errDraftFailed
		}
	}()
	return h(d)
}

// CommentAction returns the one handler of a comment action: it runs every
// registered handler, recovers panics and returns nil (fail open).
func (r *Registry) CommentAction(k CommentKind) func(sdk.CommentEvent) error {
	return func(ev sdk.CommentEvent) error {
		for _, h := range r.comments[k] {
			func() {
				defer func() { _ = recover() }()
				_ = h(ev)
			}()
		}
		return nil
	}
}

// CronHandler returns the handler of a cron job, a no-op when none registered.
// The handler's error is returned (the host logs it); a panic becomes an error.
func (r *Registry) CronHandler(name string) func(sdk.CronEvent) error {
	e, ok := r.crons[name]
	if !ok {
		return func(sdk.CronEvent) error { return nil }
	}
	return func(ev sdk.CronEvent) (err error) {
		defer func() {
			if recover() != nil {
				err = errors.New("core: cron " + name + " panicked")
			}
		}()
		return e.h(ev)
	}
}
