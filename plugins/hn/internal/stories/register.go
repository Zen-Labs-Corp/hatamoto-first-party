package stories

import (
	"hatamoto.dev/sdk"

	"github.com/Zen-Labs-Corp/hatamoto-first-party/plugins/hn/internal/core"
)

// Register adds this phase's handlers to r. plugin.go calls it once, in phase order.
func Register(r *core.Registry) {
	defineNotices()
	// routes
	r.Route("POST /vote/story/{id}", func(q *core.Request) (sdk.HTTPResponse, error) { return handleVote(r, q) })
	r.Route("POST /submit", func(q *core.Request) (sdk.HTTPResponse, error) { return handleSubmit(r, q) })
	r.Route("POST /story/{id}/edit", func(q *core.Request) (sdk.HTTPResponse, error) { return handleEdit(r, q) })
	r.Route("POST /story/{id}/delete", func(q *core.Request) (sdk.HTTPResponse, error) { return handleDelete(r, q) })
	r.Route("POST /admin/jobs", func(q *core.Request) (sdk.HTTPResponse, error) { return handleJob(r, q) })
	// pages
	for _, path := range []string{"/", "/news", "/newest", "/front", "/ask", "/show", "/jobs", "/from"} {
		r.Page(path, func(p *core.Page) error { return feedPage(r, p) })
	}
	r.Page("/submit", submitPage)
	r.PagePrefix("/item/", func(p *core.Page) error { return itemPage(r, p) })
	// cron
	r.Cron("rank", rankCron)
}
