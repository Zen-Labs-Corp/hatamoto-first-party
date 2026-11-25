package comments

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// replyFake: bob (k2) wrote cid(1), ann (k1) cid(2) under it, bob cid(3) under that; all on story eid1.
func replyFake(t *testing.T) *fake {
	f, ann, bob := newcommentsFake(t)
	f.comment(1, bob, 1)
	f.comment(2, ann, 2).Parent = cid(1)
	f.comment(3, bob, 3).Parent = cid(2)
	f.subtrees[cid(3)] = sdk.CommentSubtree{
		Ancestors: []sdk.CommentItem{item(cid(1), "", "bob", "k2", 1), item(cid(2), cid(1), "ann", "k1", 2)},
		Items:     []sdk.CommentItem{item(cid(3), cid(2), "bob", "k2", 3)},
	}
	return f
}

func replyCtx(t *testing.T, reg *core.Registry, key string, q map[string]string, blocks ...sdk.Block) sdk.PageContext {
	t.Helper()
	out, err := reg.RenderPage(sdk.PageContext{Path: "/reply", CommenterKey: key, Query: q, Blocks: blocks})
	if err != nil {
		t.Fatalf("RenderPage: %v", err)
	}
	return out
}

func replyBlock(t *testing.T, ctx sdk.PageContext) (sdk.Block, map[string]any) {
	t.Helper()
	for _, b := range ctx.Blocks {
		if b.Type == "hn_reply" {
			raw, _ := json.Marshal(b.Content)
			var c map[string]any
			_ = json.Unmarshal(raw, &c)
			return b, c
		}
	}
	t.Fatalf("no hn_reply block in %+v", ctx.Blocks)
	return sdk.Block{}, nil
}

func reply(t *testing.T, key, id string) map[string]any {
	t.Helper()
	q := map[string]string{}
	if id != "" {
		q["id"] = id
	}
	_, c := replyBlock(t, replyCtx(t, newRegistry(nil), key, q))
	return c
}

func wantNone(t *testing.T, c map[string]any, text string) {
	t.Helper()
	if c["state"] != "none" || c["text"] != text || c["row"] != nil || c["action"] != "" || c["parent"] != "" {
		t.Errorf("content: %+v", c)
	}
}

func TestReplyPageForm(t *testing.T) {
	replyFake(t)
	c := reply(t, "k1", cid(3))
	if c["state"] != "form" || c["action"] != "/-/comments/"+eid1 || c["parent"] != cid(3) || c["login_href"] != "" || c["text"] != "" {
		t.Fatalf("content: %+v", c)
	}
	row := c["row"].(map[string]any)
	for k, want := range map[string]any{
		"item_href": "/item/7", "title": "Seven", "context_link": true, "reply_href": "",
		"parent_href": "/item/7?id=" + cid(2), "context_href": "/item/7?id=" + cid(3), "vote": true, "next": "/reply?id=" + cid(3),
		"author": "bob", "id": cid(3),
	} {
		if row[k] != want {
			t.Errorf("row[%s] = %v, want %v", k, row[k], want)
		}
	}
}

func TestReplyPageKeepsPlaceholderID(t *testing.T) {
	replyFake(t)
	b, _ := replyBlock(t, replyCtx(t, newRegistry(nil), "k1", map[string]string{"id": cid(3)}, sdk.Block{ID: "r1", Type: "hn_reply"}))
	if b.ID != "r1" {
		t.Errorf("block ID %q", b.ID)
	}
}

func TestReplyPageFlagAction(t *testing.T) {
	replyFake(t)
	r := core.NewRegistry()
	r.Register("accounts", func(*core.Registry) {})
	r.Register("stories", func(*core.Registry) {})
	r.Register("comments", Register)
	r.Register("moderation", func(r *core.Registry) {
		r.RowActions(func(*core.User, core.Item) []core.Action {
			return []core.Action{{Label: "flag", Href: "/p/hn/flag/comment/x", Class: "hn-flag", Post: true}}
		})
	})
	_, c := replyBlock(t, replyCtx(t, r, "k1", map[string]string{"id": cid(3)}))
	acts, _ := c["row"].(map[string]any)["actions"].([]any)
	if len(acts) != 1 || acts[0].(map[string]any)["label"] != "flag" {
		t.Errorf("actions %v", acts)
	}
}

func TestReplyPageViewerStates(t *testing.T) {
	replyFake(t)
	c := reply(t, "k9", cid(3))
	if c["state"] != "noname" || c["login_href"] != "/login" || c["action"] != "" || c["parent"] != "" || c["row"] == nil {
		t.Errorf("noname: %+v", c)
	}
	c = reply(t, "", cid(3))
	if c["state"] != "login" || c["next"] != "/reply?id="+cid(3) || c["login_action"] == nil || c["signup_action"] == nil ||
		c["action"] != "" || c["parent"] != "" || c["row"] == nil {
		t.Errorf("signed out: %+v", c)
	}
}

func TestReplyPageNoSuchItem(t *testing.T) {
	f := replyFake(t)
	f.comment(4, nil, 4).Live = false
	f.comment(5, nil, 5).Entity = eid2 // dead story
	f.subtrees[cid(5)] = sdk.CommentSubtree{Items: []sdk.CommentItem{item(cid(5), "", "bob", "k2", 5)}}
	f.comment(6, nil, 6)
	f.subtrees[cid(6)] = sdk.CommentSubtree{Items: []sdk.CommentItem{item(cid(6), "", "bob", "k2", 6)}}
	f.subtrees[cid(6)].Items[0].Status = "dead"
	f.comment(7, nil, 7)
	f.subtreeErrFor[cid(7)] = &sdk.HostError{Code: sdk.ErrNotFound}
	for name, id := range map[string]string{
		"missing": "", "invalid": "not-a-uuid", "unknown": cid(99), "not live": cid(4), "dead story": cid(5), "dead comment": cid(6), "subtree not found": cid(7),
	} {
		t.Run(name, func(t *testing.T) { wantNone(t, reply(t, "k1", id), textNoItem) })
	}
}

func TestReplyPageHostFailures(t *testing.T) {
	f := replyFake(t)
	f.subtreeErr = &sdk.HostError{Code: sdk.ErrUnavailable}
	wantNone(t, reply(t, "k1", cid(3)), textUnavailable)
	f.subtreeErr = errBoom
	wantNone(t, reply(t, "k1", cid(3)), textBroken)
	f.subtreeErr = nil
	f.failSQL[sqlReplyParent] = errBoom
	wantNone(t, reply(t, "k1", cid(3)), textBroken)
}

func TestReplyPageDepth(t *testing.T) {
	f := replyFake(t)
	for _, n := range []int{replyMaxDepth - 2, replyMaxDepth - 1} {
		st := f.subtrees[cid(3)]
		st.Ancestors = make([]sdk.CommentItem, n)
		f.subtrees[cid(3)] = st
		c := reply(t, "k1", cid(3))
		if n == replyMaxDepth-2 && (c["state"] != "form" || c["action"] == "") {
			t.Errorf("%d ancestors: %+v", n, c)
		}
		if n == replyMaxDepth-1 && (c["state"] != "deep" || c["action"] != "" || c["parent"] != "" || c["row"] == nil || c["login_action"] != nil) {
			t.Errorf("%d ancestors: %+v", n, c)
		}
	}
	if c := reply(t, "", cid(3)); c["state"] != "deep" || c["login_action"] != nil {
		t.Errorf("signed out at max depth: %+v", c)
	}
}

func TestReplyPageLowercasesID(t *testing.T) {
	f := replyFake(t)
	id := "0000000a-0000-4000-8000-00000000000a"
	m := f.comment(8, nil, 8)
	delete(f.meta, m.ID)
	m.ID = id
	f.meta[id] = m
	f.subtrees[id] = sdk.CommentSubtree{Items: []sdk.CommentItem{item(id, "", "bob", "k2", 8)}}
	c := reply(t, "k1", strings.ToUpper(id))
	if c["state"] != "form" || c["parent"] != id || c["row"].(map[string]any)["next"] != "/reply?id="+id {
		t.Errorf("content: %+v", c)
	}
}

func replyHTML(t *testing.T, reg *core.Registry, key, id string) string {
	t.Helper()
	b, _ := replyBlock(t, replyCtx(t, reg, key, map[string]string{"id": id}))
	out := renderBlock(t, parseSections(t), b)
	return out[strings.Index(out, "</style>")+len("</style>"):] // the shared style names every class
}

func TestReplySectionForm(t *testing.T) {
	replyFake(t)
	out := replyHTML(t, newRegistry(nil), "k1", cid(3))
	for _, want := range []string{
		`action="/-/comments/` + eid1 + `"`, `data-hm-cform`, `name="hm_ct" value="__hmcf_tok__"`, `name="parent" value="` + cid(3) + `"`,
		`name="__hmcf_body__"`, `data-hn-reply`, `name="__hmcf_hp__"`, `>reply</button>`, `>context</a>`,
		`on: <a href="/item/7">Seven</a>`, `<a href="/item/7?id=` + cid(2) + `">parent</a>`, `▲`, `<p>b 0003</p>`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("missing %s in %s", want, out)
		}
	}
	for _, bad := range []string{`<textarea`, `__hmcf_name__`, `__hmcf_email__`, `>reply</a>`, `hn-login`} {
		if strings.Contains(out, bad) {
			t.Errorf("unexpected %s in %s", bad, out)
		}
	}
	if n := strings.Count(out, "data-hm-cform"); n != 1 {
		t.Errorf("%d marked forms, want 1", n)
	}
}

func TestReplySectionFlagButton(t *testing.T) {
	replyFake(t)
	r := core.NewRegistry()
	r.Register("accounts", func(*core.Registry) {})
	r.Register("stories", func(*core.Registry) {})
	r.Register("comments", Register)
	r.Register("moderation", func(r *core.Registry) {
		r.RowActions(func(*core.User, core.Item) []core.Action {
			return []core.Action{{Label: "flag", Href: "/p/hn/flag/comment/x", Class: "hn-flag", Post: true}}
		})
	})
	if out := replyHTML(t, r, "k1", cid(3)); !strings.Contains(out, `action="/p/hn/flag/comment/x"><button type="submit" class="hn-flag">flag</button>`) {
		t.Errorf("no flag button: %s", out)
	}
}

func TestReplySectionStates(t *testing.T) {
	f := replyFake(t)
	out := replyHTML(t, newRegistry(nil), "k9", cid(3))
	if !strings.Contains(out, `pick a username</a>`) || strings.Contains(out, "data-hm-cform") || !strings.Contains(out, `id="c-`+cid(3)+`"`) {
		t.Errorf("noname: %s", out)
	}
	out = replyHTML(t, newRegistry(nil), "", cid(3))
	if !strings.Contains(out, `action="`+core.LoginAction+`"`) || !strings.Contains(out, `name="next" value="/reply?id=`+cid(3)+`"`) || strings.Contains(out, "data-hm-cform") {
		t.Errorf("signed out: %s", out)
	}
	out = replyHTML(t, newRegistry(nil), "k1", "not-a-uuid")
	if !strings.Contains(out, "No such item.") || strings.Contains(out, "hn-c") || strings.Contains(out, "data-hm-cform") {
		t.Errorf("none: %s", out)
	}
	st := f.subtrees[cid(3)]
	st.Ancestors = make([]sdk.CommentItem, replyMaxDepth-1)
	f.subtrees[cid(3)] = st
	for _, key := range []string{"k1", ""} {
		out = replyHTML(t, newRegistry(nil), key, cid(3))
		if !strings.Contains(out, `id="c-`+cid(3)+`"`) || strings.Contains(out, "data-hm-cform") || strings.Contains(out, "hn-login") || strings.Contains(out, "pick a username") {
			t.Errorf("deep (%q): %s", key, out)
		}
	}
}

// A timed-out render.page hook leaves the block as the editor saved it: no state, no row.
func TestReplySectionHookTimedOut(t *testing.T) {
	b := sdk.Block{ID: "b", Type: "hn_reply", Fields: map[string]any{}}
	out := renderBlock(t, parseSections(t), b)
	out = out[strings.Index(out, "</style>")+len("</style>"):]
	if strings.Contains(out, "hn-c") || strings.Contains(out, "hn-threads") || strings.Contains(out, "data-hm-cform") {
		t.Errorf("rendered a comment row without data: %s", out)
	}
}
