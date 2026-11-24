package parity

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

const (
	nLogin   core.Notice = "saved-login"
	nMissing core.Notice = "saved-missing"
	nError   core.Notice = "saved-error"
)

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	core.DefineNotice(nLogin, "You have to be logged in to do that.")
	core.DefineNotice(nMissing, "That item does not exist.")
	core.DefineNotice(nError, "Something went wrong, please try again.")

	r.Route("POST /favorite/{kind}/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleFavorite(q, true) })
	r.Route("POST /unfavorite/{kind}/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleFavorite(q, false) })
	r.Route("POST /hide/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleHide(q, true) })
	r.Route("POST /unhide/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleHide(q, false) })
	r.RowActions(rowActions)
	r.Page("/favorites", func(p *core.Page) error { return favoritesPage(r, p) })
	// Later registrations go here; the noprocrast page filter stays last.
}
