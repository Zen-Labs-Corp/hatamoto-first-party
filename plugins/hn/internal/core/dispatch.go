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
// public (no signed-in user) non-GET request that is not same-origin, see
// SameSite, answers 403 before any handler runs; a handler error or panic
// answers 500 without the error text.
func (r *Registry) ServeHTTP(req sdk.HTTPRequest) (resp sdk.HTTPResponse, err error) {
	e, ok := r.routes[req.Method+" "+req.Pattern]
	if !ok {
		return JSONError(404, "not found"), nil
	}
	if req.Method != "GET" && req.User == nil && !SameSite(NewRequest(req)) {
		return JSONError(403, "cross-site request refused"), nil
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

// errNoName is the public text of a draft nobody gave a name.
const errNoName = "Pick a username first."

// DraftComment is the one comment.draft filter, the only hook that rejects on
// purpose, and it fails closed. Handlers run in phase order and answer with a
// DraftAnswer: any Reject (or an error carrying the SDK reject prefix) wins at
// once, whatever the phase order; otherwise the first non-empty Name becomes
// the AuthorName. When no handler names the author the draft is rejected with
// a fixed text, so a pass-through never counts as an answer and the typed name
// is never echoed. Any other handler error is returned, so the host rejects
// the draft too. A Reject text is cut to sdk.CommentDraftMaxReject bytes.
func (r *Registry) DraftComment(d sdk.CommentDraft) (sdk.CommentDraft, error) {
	name := ""
	for _, h := range r.drafts {
		ans, err := runDraft(h, d)
		if err != nil {
			if msg, ok := strings.CutPrefix(err.Error(), sdk.RejectPrefix); ok {
				ans = DraftAnswer{Reject: msg}
			} else {
				return d, err
			}
		}
		if ans.Reject != "" {
			return rejectDraft(d, ans.Reject), nil
		}
		if name == "" {
			name = ans.Name
		}
	}
	if name == "" {
		return rejectDraft(d, errNoName), nil
	}
	d.AuthorName, d.Reject = name, ""
	return d, nil
}

// rejectDraft answers a refusal: the capped text, and no author name.
func rejectDraft(d sdk.CommentDraft, msg string) sdk.CommentDraft {
	if len(msg) > sdk.CommentDraftMaxReject {
		msg = strings.ToValidUTF8(msg[:sdk.CommentDraftMaxReject], "")
	}
	d.AuthorName, d.Reject = "", msg
	return d
}

func runDraft(h func(sdk.CommentDraft) (DraftAnswer, error), d sdk.CommentDraft) (out DraftAnswer, err error) {
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
