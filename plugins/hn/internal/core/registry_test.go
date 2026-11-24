package core_test

import (
	"encoding/json"
	"errors"
	"os"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

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

type draftFn = func(sdk.CommentDraft) (core.DraftAnswer, error)

func answer(a core.DraftAnswer) draftFn {
	return func(sdk.CommentDraft) (core.DraftAnswer, error) { return a, nil }
}

// draftWith registers one handler per phase named in hs.
func draftWith(hs map[string]draftFn) *core.Registry {
	r := core.NewRegistry()
	fns := map[string]func(*core.Registry){}
	for ph, h := range hs {
		fns[ph] = func(r *core.Registry) { r.Draft(h) }
	}
	registerAll(r, fns)
	return r
}

func TestRegistryDraftNoNameIsRejected(t *testing.T) {
	typed := sdk.CommentDraft{CommenterKey: "k", AuthorName: "typed-name"}
	for name, r := range map[string]*core.Registry{
		"no handler":   draftWith(nil),
		"pass-through": draftWith(map[string]draftFn{"accounts": answer(core.DraftAnswer{})}),
	} {
		out, err := r.DraftComment(typed)
		if err != nil || out.Reject != "Pick a username first." || out.AuthorName != "" {
			t.Errorf("%s: got %+v, %v", name, out, err)
		}
	}
}

func TestRegistryDraftRejectWinsInAnyOrder(t *testing.T) {
	no := answer(core.DraftAnswer{Reject: "Banned."})
	ann := answer(core.DraftAnswer{Name: "ann"})
	for name, hs := range map[string]map[string]draftFn{
		"name first":   {"accounts": ann, "stories": no},
		"name last":    {"accounts": no, "stories": ann},
		"name between": {"accounts": no, "stories": ann, "comments": no},
	} {
		out, err := draftWith(hs).DraftComment(sdk.CommentDraft{AuthorName: "x"})
		if err != nil || out.Reject != "Banned." || out.AuthorName != "" {
			t.Errorf("%s: got %+v, %v", name, out, err)
		}
	}
	r := draftWith(map[string]draftFn{"accounts": func(sdk.CommentDraft) (core.DraftAnswer, error) {
		return core.DraftAnswer{}, errors.New(sdk.RejectPrefix + "No.")
	}})
	if out, err := r.DraftComment(sdk.CommentDraft{}); err != nil || out.Reject != "No." {
		t.Errorf("reject error: %+v, %v", out, err)
	}
}

func TestRegistryDraftRejectIsCapped(t *testing.T) {
	long := strings.Repeat("é", 300)
	for name, h := range map[string]draftFn{
		"reject": answer(core.DraftAnswer{Reject: long}),
		"reject error": func(sdk.CommentDraft) (core.DraftAnswer, error) {
			return core.DraftAnswer{}, errors.New(sdk.RejectPrefix + long)
		},
	} {
		out, _ := draftWith(map[string]draftFn{"accounts": h}).DraftComment(sdk.CommentDraft{})
		if len(out.Reject) == 0 || len(out.Reject) > sdk.CommentDraftMaxReject || !utf8.ValidString(out.Reject) {
			t.Errorf("%s: %d bytes, valid %v", name, len(out.Reject), utf8.ValidString(out.Reject))
		}
	}
}

func TestRegistryDraftFirstNameWins(t *testing.T) {
	r := draftWith(map[string]draftFn{
		"accounts": answer(core.DraftAnswer{}),
		"stories":  answer(core.DraftAnswer{Name: "ann"}),
		"comments": answer(core.DraftAnswer{Name: "bob"}),
	})
	out, err := r.DraftComment(sdk.CommentDraft{AuthorName: "x"})
	if err != nil || out.AuthorName != "ann" || out.Reject != "" {
		t.Errorf("got %+v, %v", out, err)
	}
}

func TestRegistryDraftErrorAndPanicFailClosed(t *testing.T) {
	for name, h := range map[string]draftFn{
		"error": func(sdk.CommentDraft) (core.DraftAnswer, error) {
			return core.DraftAnswer{Name: "ann"}, errors.New("db down")
		},
		"panic": func(sdk.CommentDraft) (core.DraftAnswer, error) { panic("boom") },
	} {
		if _, err := draftWith(map[string]draftFn{"accounts": h}).DraftComment(sdk.CommentDraft{}); err == nil {
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

func TestRegistryServeRefusesCrossSitePublicPost(t *testing.T) {
	ran := 0
	r := core.NewRegistry()
	registerAll(r, map[string]func(*core.Registry){"accounts": func(r *core.Registry) {
		h := func(*core.Request) (sdk.HTTPResponse, error) { ran++; return sdk.HTTPResponse{Status: 204}, nil }
		r.Route("POST /p", h)
		r.Route("GET /g", h)
		r.Route("POST /admin/x", h)
	}})
	var serveKey func(method, pat, site string, user *sdk.HTTPUser, key string) sdk.HTTPResponse
	serve := func(method, pat, site string, user *sdk.HTTPUser) sdk.HTTPResponse {
		return serveKey(method, pat, site, user, "")
	}
	serveKey = func(method, pat, site string, user *sdk.HTTPUser, key string) sdk.HTTPResponse {
		req := sdk.HTTPRequest{Method: method, Pattern: pat, User: user, CommenterKey: key}
		if site != "" {
			req.Headers = map[string]string{"sec-fetch-site": site}
		}
		resp, err := r.ServeHTTP(req)
		if err != nil {
			t.Fatal(err)
		}
		return resp
	}
	for _, site := range []string{"cross-site", "same-site"} {
		resp := serve("POST", "/p", site, nil)
		if resp.Status != 403 || resp.Headers["Cache-Control"] != "no-store" || ran != 0 {
			t.Errorf("%s: %+v, handler ran %d times", site, resp, ran)
		}
	}
	for _, site := range []string{"", "same-origin", "none"} {
		if resp := serve("POST", "/p", site, nil); resp.Status != 204 {
			t.Errorf("%q: status %d", site, resp.Status)
		}
	}
	if resp := serve("GET", "/g", "cross-site", nil); resp.Status != 204 {
		t.Errorf("GET cross-site: status %d", resp.Status)
	}
	before := ran
	if resp := serveKey("POST", "/p", "cross-site", nil, "k1"); resp.Status != 403 || ran != before {
		t.Errorf("commenter key: status %d, ran %d", resp.Status, ran-before)
	}
	if resp := serve("POST", "/p", "cross-site", &sdk.HTTPUser{}); resp.Status != 403 || ran != before {
		t.Errorf("public POST with user: status %d, ran %d", resp.Status, ran-before)
	}
	if resp := serve("POST", "/admin/x", "cross-site", &sdk.HTTPUser{}); resp.Status != 204 {
		t.Errorf("admin POST with user: status %d", resp.Status)
	}
	if resp := serve("POST", "/admin/x", "cross-site", nil); resp.Status != 403 {
		t.Errorf("admin POST without user: status %d", resp.Status)
	}
}

// TestAdminPrefixMatchesManifest pins the dispatcher's rule (non-public means
// under /admin/) to plugin.json, so a new non-public route elsewhere fails here.
func TestAdminPrefixMatchesManifest(t *testing.T) {
	raw, err := os.ReadFile("../../plugin.json")
	if err != nil {
		t.Fatal(err)
	}
	var m struct {
		Routes []struct {
			Method, Path string
			Public       bool
		}
	}
	if err := json.Unmarshal(raw, &m); err != nil {
		t.Fatal(err)
	}
	if len(m.Routes) == 0 {
		t.Fatal("no routes in plugin.json")
	}
	for _, rt := range m.Routes {
		if strings.HasPrefix(rt.Path, "/admin/") == rt.Public {
			t.Errorf("%s %s: public=%v does not match the /admin/ prefix rule", rt.Method, rt.Path, rt.Public)
		}
	}
}
