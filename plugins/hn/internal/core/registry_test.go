package core_test

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

func mustPanic(t *testing.T, name string, fn func()) {
	t.Helper()
	defer func() {
		if recover() == nil {
			t.Errorf("%s did not panic", name)
		}
	}()
	fn()
}

// registerAll registers the phases in order; fns[phase] may be nil.
func registerAll(r *core.Registry, fns map[string]func(*core.Registry)) {
	for _, p := range core.Phases {
		fn := fns[p]
		if fn == nil {
			fn = func(*core.Registry) {}
		}
		r.Register(p, fn)
	}
}

func marker(id string) func(*core.Page) error {
	return func(p *core.Page) error { p.Append(sdk.Block{ID: id, Type: id}); return nil }
}

func ids(blocks []sdk.Block) string {
	var s []string
	for _, b := range blocks {
		s = append(s, b.ID)
	}
	return strings.Join(s, ",")
}

func TestRegistryTwoPhasesShareOneDispatcher(t *testing.T) {
	var calls []string
	phase := func(name string) func(*core.Registry) {
		return func(r *core.Registry) {
			r.Page("/news", marker(name))
			r.Comment(core.OnCreated, func(sdk.CommentEvent) error { calls = append(calls, name); return nil })
			r.Route("POST /"+name, func(*core.Request) (sdk.HTTPResponse, error) {
				return sdk.HTTPResponse{Status: 200, Body: name}, nil
			})
		}
	}
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){"accounts": phase("a"), "stories": phase("s")})

	out, err := r.RenderPage(sdk.PageContext{Path: "/news"})
	if err != nil || ids(out.Blocks) != "a,s" {
		t.Errorf("RenderPage blocks %q, %v", ids(out.Blocks), err)
	}
	if out, _ := r.RenderPage(sdk.PageContext{Path: "/other"}); len(out.Blocks) != 0 {
		t.Errorf("other path got blocks %q", ids(out.Blocks))
	}
	if err := r.CommentAction(core.OnCreated)(sdk.CommentEvent{}); err != nil || !reflect.DeepEqual(calls, []string{"a", "s"}) {
		t.Errorf("CommentAction calls %v, %v", calls, err)
	}
	for _, n := range []string{"a", "s"} {
		resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: "POST", Pattern: "/" + n})
		if err != nil || resp.Body != n {
			t.Errorf("route %s: %+v, %v", n, resp, err)
		}
	}
}

func TestRegistryPagePrefixAndAll(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){"accounts": func(r *core.Registry) {
		r.PageAll(marker("all"))
		r.PagePrefix("/item/", marker("item"))
	}})
	for path, want := range map[string]string{"/item/12": "all,item", "/item/": "all", "/news": "all"} {
		out, _ := r.RenderPage(sdk.PageContext{Path: path})
		if got := ids(out.Blocks); got != want {
			t.Errorf("%s: blocks %q, want %q", path, got, want)
		}
	}
}

func TestRegistryPhaseOrderKept(t *testing.T) {
	r := core.NewRegistry()
	mustPanic(t, "stories before accounts", func() { r.Register("stories", func(*core.Registry) {}) })
	registerAll(core.NewRegistry(), nil)
	mustPanic(t, "seventh phase", func() {
		r := core.NewRegistry()
		registerAll(r, nil)
		r.Register("accounts", func(*core.Registry) {})
	})
}

func TestRegistryDuplicateRoutePanics(t *testing.T) {
	r := core.NewRegistry()
	h := func(*core.Request) (sdk.HTTPResponse, error) { return sdk.HTTPResponse{}, nil }
	mustPanic(t, "duplicate route", func() {
		registerAll(r, map[string]func(*core.Registry){
			"accounts": func(r *core.Registry) { r.Route("POST /x", h) },
			"stories":  func(r *core.Registry) { r.Route("POST /x", h) },
		})
	})
	mustPanic(t, "key without method", func() { core.NewRegistry().Route("/x", h) })
}

func TestRegistryEmptyReturnsInputUnchanged(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, nil)

	ctx := sdk.PageContext{Path: "/news", Blocks: []sdk.Block{{ID: "b", Type: "t"}}, Hide: []string{"x"}}
	if out, err := r.RenderPage(ctx); err != nil || !reflect.DeepEqual(out, ctx) {
		t.Errorf("RenderPage %+v, %v", out, err)
	}
	chk := sdk.CommentCheck{Body: "hi", Score: 2, Reasons: []string{"r"}}
	if out, err := r.CheckComment(chk); err != nil || !reflect.DeepEqual(out, chk) {
		t.Errorf("CheckComment %+v, %v", out, err)
	}
	dr := sdk.CommentDraft{CommenterKey: "k", AuthorName: "n"}
	if out, err := r.DraftComment(dr); err != nil || out != dr {
		t.Errorf("DraftComment %+v, %v", out, err)
	}
	if resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: "GET", Pattern: "/nope"}); err != nil || resp.Status != 404 {
		t.Errorf("ServeHTTP %+v, %v", resp, err)
	}
	if err := r.CronHandler("rank")(sdk.CronEvent{}); err != nil {
		t.Errorf("CronHandler: %v", err)
	}
	if err := r.CommentAction(core.OnUpdated)(sdk.CommentEvent{}); err != nil {
		t.Errorf("CommentAction: %v", err)
	}
}

func TestRegistryCheckNeverErrors(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) {
			r.Check(func(c sdk.CommentCheck) (sdk.CommentCheck, error) { return c, errors.New("boom") })
			r.Check(func(sdk.CommentCheck) (sdk.CommentCheck, error) { panic("boom") })
		},
		"stories": func(r *core.Registry) {
			r.Check(func(c sdk.CommentCheck) (sdk.CommentCheck, error) {
				c.Score += 3
				c.Reasons = append(c.Reasons, "three")
				return c, nil
			})
		},
		"comments": func(r *core.Registry) {
			r.Check(func(c sdk.CommentCheck) (sdk.CommentCheck, error) { c.Score = -5; return c, nil })
		},
		"moderation": func(r *core.Registry) {
			r.Check(func(c sdk.CommentCheck) (sdk.CommentCheck, error) {
				c.Score += 2
				c.Reasons = append(c.Reasons, "two")
				return c, nil
			})
		},
	})
	out, err := r.CheckComment(sdk.CommentCheck{Body: "x"})
	if err != nil || out.Score != 5 || !reflect.DeepEqual(out.Reasons, []string{"three", "two"}) {
		t.Errorf("got %+v, %v", out, err)
	}
}

func TestRegistryDraftRejectWins(t *testing.T) {
	name := func(n string) func(sdk.CommentDraft) (sdk.CommentDraft, error) {
		return func(d sdk.CommentDraft) (sdk.CommentDraft, error) { d.AuthorName = n; return d, nil }
	}
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) { r.Draft(name("ann")) },
		"stories": func(r *core.Registry) {
			r.Draft(func(d sdk.CommentDraft) (sdk.CommentDraft, error) { d.Reject = "Pick a username first."; return d, nil })
		},
		"comments": func(r *core.Registry) { r.Draft(name("later")) },
	})
	out, err := r.DraftComment(sdk.CommentDraft{CommenterKey: "k", AuthorName: "x"})
	if err != nil || out.Reject != "Pick a username first." {
		t.Errorf("got %+v, %v", out, err)
	}

	r = core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) {
			r.Draft(func(d sdk.CommentDraft) (sdk.CommentDraft, error) {
				return d, errors.New(sdk.RejectPrefix + "No.")
			})
		},
	})
	out, err = r.DraftComment(sdk.CommentDraft{})
	if err != nil || out.Reject != "No." {
		t.Errorf("reject error: %+v, %v", out, err)
	}
}

func TestRegistryDraftFirstNameWins(t *testing.T) {
	name := func(n string) func(sdk.CommentDraft) (sdk.CommentDraft, error) {
		return func(d sdk.CommentDraft) (sdk.CommentDraft, error) { d.AuthorName = n; return d, nil }
	}
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) {
			r.Draft(func(d sdk.CommentDraft) (sdk.CommentDraft, error) { return d, nil })
			r.Draft(name("ann"))
		},
		"stories": func(r *core.Registry) { r.Draft(name("bob")) },
	})
	out, err := r.DraftComment(sdk.CommentDraft{})
	if err != nil || out.AuthorName != "ann" {
		t.Errorf("got %+v, %v", out, err)
	}
}

func TestRegistryDraftErrorAndPanicFailClosed(t *testing.T) {
	for name, h := range map[string]func(sdk.CommentDraft) (sdk.CommentDraft, error){
		"error": func(d sdk.CommentDraft) (sdk.CommentDraft, error) { return d, errors.New("db down") },
		"panic": func(sdk.CommentDraft) (sdk.CommentDraft, error) { panic("boom") },
	} {
		r := core.NewRegistry()
		registerAll(r, map[string]func(*core.Registry){"accounts": func(r *core.Registry) { r.Draft(h) }})
		if _, err := r.DraftComment(sdk.CommentDraft{}); err == nil {
			t.Errorf("%s: draft not refused", name)
		}
	}
}

func TestRegistryPageFailOpen(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) { r.Page("/news", marker("a")) },
		"stories": func(r *core.Registry) {
			r.Page("/news", func(p *core.Page) error {
				p.Append(sdk.Block{ID: "bad"})
				p.Ctx.Hide = append(p.Ctx.Hide, "bad")
				return errors.New("boom")
			})
		},
		"comments": func(r *core.Registry) {
			r.Page("/news", func(p *core.Page) error {
				p.Prepend(sdk.Block{ID: "worse"})
				p.Ctx.SetCookies = append(p.Ctx.SetCookies, sdk.SetCookie{Name: "c"})
				panic("boom")
			})
		},
		"moderation": func(r *core.Registry) { r.Page("/news", marker("m")) },
	})
	out, err := r.RenderPage(sdk.PageContext{Path: "/news"})
	if err != nil || ids(out.Blocks) != "a,m" || len(out.Hide) != 0 || len(out.SetCookies) != 0 {
		t.Errorf("got blocks %q hide %v cookies %v, %v", ids(out.Blocks), out.Hide, out.SetCookies, err)
	}
}

func TestRegistryHTTPErrorIs500WithoutText(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){"accounts": func(r *core.Registry) {
		r.Route("GET /err", func(*core.Request) (sdk.HTTPResponse, error) { return sdk.HTTPResponse{}, errors.New("secret detail") })
		r.Route("GET /panic", func(*core.Request) (sdk.HTTPResponse, error) { panic("secret detail") })
	}})
	for _, p := range []string{"/err", "/panic"} {
		resp, err := r.ServeHTTP(sdk.HTTPRequest{Method: "GET", Pattern: p})
		if err != nil || resp.Status != 500 || resp.Body != `{"error":"internal"}` || resp.Headers["Cache-Control"] != "no-store" || strings.Contains(resp.Body, "secret") {
			t.Errorf("%s: %+v, %v", p, resp, err)
		}
	}
}

func TestRegistryGatesFirstErrorWins(t *testing.T) {
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) { r.Gate(core.GateSubmit, func(*core.GateReq) error { return nil }) },
		"stories": func(r *core.Registry) {
			r.Gate(core.GateSubmit, func(*core.GateReq) error { return errors.New("first") })
		},
		"comments": func(r *core.Registry) {
			r.Gate(core.GateSubmit, func(*core.GateReq) error { return errors.New("second") })
		},
		"moderation": func(r *core.Registry) {
			r.Gate(core.GateFlag, func(*core.GateReq) error { panic("boom") })
		},
	})
	if err := r.RunGates(core.GateSubmit, &core.GateReq{}); err == nil || err.Error() != "first" {
		t.Errorf("err = %v", err)
	}
	if err := r.RunGates(core.GateVote, &core.GateReq{}); err != nil {
		t.Errorf("no gates: %v", err)
	}
	if err := r.RunGates(core.GateFlag, &core.GateReq{}); err == nil || strings.Contains(err.Error(), "boom") {
		t.Errorf("panicking gate: %v", err)
	}
}

func TestRegistryCronUnknownNamePanics(t *testing.T) {
	mustPanic(t, "unknown cron", func() {
		registerAll(core.NewRegistry(), map[string]func(*core.Registry){
			"accounts": func(r *core.Registry) { r.Cron("nope", func(sdk.CronEvent) error { return nil }) },
		})
	})
	mustPanic(t, "duplicate cron", func() {
		registerAll(core.NewRegistry(), map[string]func(*core.Registry){
			"accounts": func(r *core.Registry) { r.Cron("rank", func(sdk.CronEvent) error { return nil }) },
			"stories":  func(r *core.Registry) { r.Cron("rank", func(sdk.CronEvent) error { return nil }) },
		})
	})
	r := core.NewRegistry()
	boom := errors.New("boom")
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) { r.Cron("rank", func(sdk.CronEvent) error { return boom }) },
		"stories":  func(r *core.Registry) { r.Cron("sweep", func(sdk.CronEvent) error { panic("x") }) },
	})
	if err := r.CronHandler("rank")(sdk.CronEvent{}); !errors.Is(err, boom) {
		t.Errorf("rank: %v", err)
	}
	if err := r.CronHandler("sweep")(sdk.CronEvent{}); err == nil {
		t.Error("panicking cron returned nil")
	}
}

func TestRegistryRoutesListsPhase(t *testing.T) {
	h := func(*core.Request) (sdk.HTTPResponse, error) { return sdk.HTTPResponse{}, nil }
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"accounts": func(r *core.Registry) { r.Route("POST /username", h) },
		"stories":  func(r *core.Registry) { r.Route("POST /submit", h); r.Route("GET /x", h) },
	})
	want := []core.RouteInfo{{Key: "POST /username", Phase: "accounts"}, {Key: "POST /submit", Phase: "stories"}, {Key: "GET /x", Phase: "stories"}}
	if got := r.Routes(); !reflect.DeepEqual(got, want) {
		t.Errorf("Routes() = %v", got)
	}
}

func TestRegistryActionsAndEvents(t *testing.T) {
	var seen []string
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){
		"stories": func(r *core.Registry) {
			r.RowActions(func(*core.User, core.Item) []core.Action { return []core.Action{{Label: "one"}} })
			r.Subscribe(core.StorySaved, func(core.StoryEvent) { panic("boom") })
		},
		"moderation": func(r *core.Registry) {
			r.RowActions(func(*core.User, core.Item) []core.Action { panic("boom") })
			r.RowActions(func(*core.User, core.Item) []core.Action { return []core.Action{{Label: "two"}} })
		},
		"parity": func(r *core.Registry) {
			r.Subscribe(core.StorySaved, func(ev core.StoryEvent) { seen = append(seen, ev.Title) })
		},
	})
	acts := r.ActionsFor(nil, core.Item{})
	if len(acts) != 2 || acts[0].Label != "one" || acts[1].Label != "two" {
		t.Errorf("ActionsFor = %v", acts)
	}
	r.Publish(core.StoryEvent{Kind: core.StorySaved, Title: "t"})
	r.Publish(core.StoryEvent{Kind: core.StoryDeleted, Title: "gone"})
	if !reflect.DeepEqual(seen, []string{"t"}) {
		t.Errorf("events %v", seen)
	}
}
