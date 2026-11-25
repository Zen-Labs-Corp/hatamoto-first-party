package accounts

import (
	"net/url"
	"slices"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
	"hatamoto.dev/sdk"
)

// state is the viewer's standing: "named" (has a username), "noname" (signed
// in, no username yet) or "out". A host error reads as signed out or noname
// (fail open).
func state(p *core.Page) (string, *core.User) {
	if v := p.Viewer(); v != nil {
		return "named", v
	}
	if p.Ctx.CommenterKey != "" {
		return "noname", nil
	}
	return "out", nil
}

func userHref(name string) string { return "/user?id=" + url.QueryEscape(name) }

// userbar puts the user bar first on every filtered page.
func userbar(p *core.Page) error {
	st, v := state(p)
	c := map[string]any{"state": st, "login_href": "/login", "logout_action": logoutAction}
	if v != nil {
		c["name"], c["karma"], c["user_href"] = v.Username, v.Karma, userHref(v.Username)
	}
	if i := p.Find("hn_userbar"); i >= 0 {
		p.Ctx.Blocks = slices.Delete(p.Ctx.Blocks, i, i+1)
	}
	p.Prepend(sdk.Block{ID: "hn_userbar", Type: "hn_userbar", Content: c})
	return nil
}

// place fills the page's placeholder block of that type, keeping its ID, or
// appends the block when the page has none.
func place(p *core.Page, typ string, c map[string]any) {
	i, id := p.Find(typ), typ
	if i >= 0 && p.Ctx.Blocks[i].ID != "" {
		id = p.Ctx.Blocks[i].ID
	}
	p.Replace(i, sdk.Block{ID: id, Type: typ, Content: c})
}

func loginPage(p *core.Page) error {
	st, v := state(p)
	c := map[string]any{
		"state": st, "notice": core.NoticeText(p.Query("m")),
		"next": "/login", "login_action": loginAction, "signup_action": signupAction,
		"logout_action": logoutAction, "email_signin_href": signInURL,
	}
	if v != nil {
		c["name"] = v.Username
	}
	place(p, "hn_login", c)
	return nil
}

func userPage(p *core.Page) error {
	c := map[string]any{"found": false, "missing": true, "notice": core.NoticeText(p.Query("m"))}
	if id := p.Query("id"); id != "" {
		u, err := core.UserByName(id)
		if err != nil {
			return err // the registry undoes this handler: the placeholder stays
		}
		if u != nil {
			fill(c, u, p.Viewer())
		}
	}
	place(p, "hn_user", c)
	return nil
}

// fill adds the profile of u to c; the owner also gets the edit form values.
// Only plain values go in: never the user row or its commenter key.
func fill(c map[string]any, u *core.User, viewer *core.User) {
	own := viewer != nil && viewer.ID == u.ID
	c["found"], c["missing"], c["name"], c["karma"], c["own"] = true, false, u.Username, u.Karma, own
	c["created"] = u.CreatedAt.UTC().Format("January 2, 2006")
	c["about"] = aboutSegments(u.About)
	q := url.QueryEscape(u.Username)
	c["submissions_href"] = "/newest?id=" + q
	c["comments_href"] = "/threads?id=" + q
	c["favorites_href"] = "/favorites?id=" + q
	if !own {
		return
	}
	c["about_raw"], c["showdead"], c["noprocrast"] = u.About, u.Showdead, u.Noprocrast
	c["maxvisit"], c["minaway"] = u.Maxvisit, u.Minaway
	c["next"] = userHref(u.Username)
	// A failed read offers neither the password forms nor connect: the member may already have a password.
	l, err := loginByUser(u.ID)
	if err != nil {
		c["has_password"] = false
		return
	}
	c["has_password"] = l != nil
	if l != nil {
		c["email"] = l.Email
		c["password_action"], c["logout_all_action"] = passwordAction, logoutAll
	} else {
		c["connect_action"] = connectAction
	}
}
