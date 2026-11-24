# hn

A Hacker News style community: stories, votes, karma, ranked threads, flags and moderation on top of Hatamoto's core comments.

Status: 0.1.0 is the skeleton. It declares the whole plugin map (permissions, routes, pages, cron jobs, settings, content type), ships every migration and wires one package per phase through a single registry. Behaviour arrives phase by phase.

## Layout

```
plugin.go                  one guest handler per export, dispatching through core.Registry
exports_wasip1.go          the //go:wasmexport lines (14 exports)
host_wasip1.go             sets core.Call to the host call of the guest package
main_other.go              main for host vet and test
internal/core/             shared seam: registry, host calls, viewer, requests, settings, HN rules
internal/accounts/         P1  username claim, profile, karma, user bar, login
internal/stories/          P2  submit, story votes, gravity rank, story lists
internal/comments/         P3  comment votes, comment rank, karma credit, /threads
internal/moderation/       P4  flags, vouches, karma gates, bans, rate limits, admin page
internal/parity/           P5  favorites, hide, search, RSS, JSON API, noprocrast
internal/polish/           P6  votes without a reload, mobile pass
migrations/                one numbered block per phase, see SCHEMA.md
schemas/hn-story.json      the one content type the plugin owns
partials/  ui/             shared style partial, admin page
```

Why: the host lets a plugin register one handler per export, so six phases cannot each register their own. They register into one `core.Registry` instead and `plugin.go` installs one dispatcher per export, in a fixed phase order (accounts, stories, comments, moderation, parity, polish). One package per phase keeps their files apart. Go packages under `internal/` build into the wasip1 c-shared module (checked by `scripts/build-plugin.sh`). If a toolchain ever refuses that, the fallback is flat `package main` files prefixed with the phase name (`accounts_*.go`, ...).

Rules: no phase imports another phase (a test checks it), shared code goes in `internal/core`, and new tables go only in the phase's own migration block.

## Pages

`render.page` does not run on a 404, so every path below except `/` must exist as a CMS page holding the section named here. Kit page slugs: `news newest front newcomments best ask show jobs submit threads user from favorites login search`, plus a plain `guidelines` page. `/` is served when the owner makes the news page the homepage. Story pages come from the plugin-owned `hn-story` content type; the slug is the story number. `/item/<number>` is the canonical story URL the plugin links to. The `/item/{slug}` url_pattern is not carried by the plugin package (a schema file holds no settings, so core gives the type the default `/{slug}`): the hn kit sets it on the type, or the owner sets it in Settings > Content types. Without it stories route at `/<number>` and the plugin's `/item/` links do not resolve.

| Page path | Section the page holds | Phase | Query keys used |
|---|---|---|---|
| `/` `/news` `/newest` `/front` `/ask` `/show` `/jobs` `/best` `/from` | `hn-feed` (the feed is the path) | P2 | `p`, `next`, `day` (front), `site` (from), `id` (newest by author) |
| `/submit` | `hn-submit` (edit mode with `?id=<story>`) | P2 | `id` |
| `/item/<number>` | `hn-item`, injected by the filter | P2 | `id` (a comment id: focus view) |
| `/threads` `/newcomments` | `hn-threads` | P3 | `id` (user), `p` |
| `/user` | `hn_user` | P1 | `id` |
| `/login` | `hn_login` | P1 | none |
| `/favorites` | `hn_saved` (favorites; `view=hidden` is the member's hidden stories) | P5 | `id`, `view`, `p` |
| `/search` | `hn_search` | P5 | `q`, `p` |
| every filtered page | `hn_userbar`, injected first | P1 | |

Sections: `hn-feed hn-submit hn-item hn-threads hn_user hn_login hn_saved hn_search hn_userbar` (`hn-item` and `hn_userbar` are injected, not placed). Declared page paths: 16 (the limit), prefix `/item/`. Query keys (9): `p id day site kind next view q m` (`m` is the notice code of a redirect, read by every filtered page).

The noprocrast notice is rendered by `hn_saved` as well: a member with the flag on who is past their visit window sees the account bar and that notice instead of the page (no extra section to place).

## Feeds and API

Public routes of the parity phase, served at `/p/hn/...`.

- `GET /p/hn/rss?feed=news|newest|ask|show|jobs|best` (default `news`): RSS 2.0, 30 items, root-relative links (the plugin does not know the site address), `Cache-Control: public, max-age=60`. An unknown feed is a 404.
- `GET /p/hn/api/feed/{name}?p=N` answers `{"items":[...],"page":N,"has_more":bool}`; `GET /p/hn/api/item/{number}` answers `{"id","type","title","url","text","by","points","comments","created_at"}` (`id` is the item number as a string); `GET /p/hn/api/user/{name}` answers `{"name","created_at","karma","about"}`. An unknown feed, item or user is a 404 `{"error":"not found"}`; success sends `Cache-Control: public, max-age=30`. The API is read-only and shows only what a signed-out visitor sees (no dead or hidden items, no keys, no emails). Field names are final once released.
- Favorites and hide: `POST /p/hn/favorite/{story|comment}/{id}` and `POST /p/hn/unfavorite/{story|comment}/{id}` (a story id is the item number, a comment id is the core comment id), `POST /p/hn/hide/{number}` and `POST /p/hn/unhide/{number}`. Story rows get the hide and favorite actions through the row-action seam; a theme may print the comment favorite form itself.
- Search: a full-text index of story titles and text and of comment text, kept by story and comment events and repaired by the `search` cron (every 5 minutes).

## Theme-posted forms

A theme never calls plugin partials or functions (a theme template naming an undefined plugin template fails to parse on a site without the plugin). The theme prints these as plain forms; `{id}` is the core comment id. The routes are served at `/p/hn/...`.

| Form the theme prints | Method and path | Fields | Phase |
|---|---|---|---|
| up-arrow (class `hn-vote-form`), not printed for the viewer's own comment | `POST /p/hn/vote/comment/{id}` | `how` = `up`, `next` | P3 |
| `flag` button | `POST /p/hn/flag/comment/{id}` | `next` | P4 |
| `vouch` button, printed only when the comment is dead | `POST /p/hn/vouch/comment/{id}` | `next` | P4 |

Handler rules:

- `next` is honoured only as a same-site absolute path (one leading `/`, no `//`, no scheme); otherwise the `Referer` path when the browser says the post is same-origin; otherwise `/`.
- The answer is a 303 to that path with `?m=<code>` (a notice code, never free text).
- A visitor with no commenter key or no username gets a 303 to `/login` with a notice.
- Karma gates, own comments, repeat votes and dead rules are enforced by the route; the theme shows the controls to everyone it cannot judge.
- `Accept: application/json` answers `{points, voted}` instead of redirecting.
- A cross-site post is refused (`Sec-Fetch-Site`): the dispatcher answers 403 (JSON error, no-store) to any public non-GET request whose header is not absent, `same-origin` or `none`, before the route runs.

## Routes

Public routes are served at `/p/hn/...`; admin routes need the owner.

| Route | Access | Phase |
|---|---|---|
| `POST /username`, `POST /profile` | public | P1 |
| `POST /submit`, `POST /story/{id}/edit`, `POST /story/{id}/delete`, `POST /vote/story/{id}` | public | P2 |
| `POST /admin/jobs` | admin | P2 |
| `POST /vote/comment/{id}` | public | P3 |
| `POST /flag/{kind}/{id}`, `POST /vouch/{kind}/{id}` | public | P4 |
| `GET /admin/queue`, `GET /admin/log`, `POST /admin/users/{name}/ban`, `POST /admin/users/{name}/unban`, `POST /admin/domains`, `POST /admin/domains/remove`, `POST /admin/nets/ban`, `POST /admin/nets/unban`, `POST /admin/items/kill`, `POST /admin/items/restore` | admin | P4 |
| `POST /favorite/{kind}/{id}`, `POST /unfavorite/{kind}/{id}`, `POST /hide/{id}`, `POST /unhide/{id}` | public | P5 |
| `GET /rss`, `GET /api/feed/{name}`, `GET /api/item/{id}`, `GET /api/user/{name}` | public | P5 |
| `GET /state/{entity_id}` | public | P6 |

## Cron

| Job | Every | Phase | Does |
|---|---|---|---|
| `rank` | 1m | P2 | gravity rank of stories |
| `comment_rank` | 1m | P3 | writes the comment order in batches |
| `reconcile` | 1h | P3 | orphan sweep, karma recount |
| `sweep` | 1h | P4 | expires bans, trims rate events |
| `search` | 5m | P5 | search index reconcile |

## Permissions

Declared once in 0.1.0: adding one later makes every install grant again.

| Permission | Used by | Why |
|---|---|---|
| `db` | all | votes, karma, ranks, lists, flags and the search mirror, in the plugin's own database area |
| `settings` | all | the thresholds below |
| `site.scripts` | P6 | `assets/plugin.js`: votes without a reload; everything works without it |
| `entities.write.own` | P2 | creates, edits and deletes stories of the one content type `hn-story` |
| `comments.read` | P1, P3, P4, P5 | comment events, a member's comments, the commenter key on pages and routes |
| `comments.moderate` | P1, P3, P4 | marks comments dead or restores them, sets their order |
| `client.key` | P1, P4 | pseudonymous address and network keys, for rate limits and network bans |

In plain words, the three that warn on install:

- Comments, moderate: marks comments on the plugin's own content type dead or restores them and sets their order, without review. It cannot post comments or touch other content types. Logged in the audit log.
- Content, write (own type): creates, edits and deletes entries of one content type it declares and publishes them at once, without review. Rate limited and logged. It cannot touch any other content.
- Visitor keys: a pseudonymous key for each visitor's address and network, not the address itself. The key is stable, so the plugin can recognise a returning visitor and block a network. It is personal data: your privacy notice should say so.

No `http.fetch`, `kv`, `mail.send`, `log` or `entities.read`: the lists read the plugin's own copy of each story.

`request_headers` declares `sec-fetch-site` (not a permission): routes use it to refuse cross-site form posts.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `flag_karma` | 30 | karma needed to flag |
| `downvote_karma` | 500 | karma needed to downvote a comment |
| `vouch_karma` | 31 | karma needed to vouch for a dead item |
| `edit_window_minutes` | 120 | how long an author may edit or delete a story |
| `submit_per_hour` | 6 | stories one member may submit per hour |
| `flags_to_dead` | 5 | flags after which an item is marked dead |
| `page_size` | 30 | stories per page (1 to 100) |

## Data

See [SCHEMA.md](SCHEMA.md). The plugin only touches its own database area.

## Failure handling

Hooks fail open: a handler that errors or panics is skipped and never fails a render or a request (a route answers a generic 500 without the error text). `comment.check` never returns an error, because the host holds a comment on any error. `comment.draft` is the one hook that fails closed: handlers answer with a canonical `Name` or a `Reject`; any reject wins (text cut to 200 bytes), and a draft nobody named is rejected with a fixed text (`Pick a username first.`) without echoing the typed name. With no handler, or only pass-through handlers, every draft is rejected. Any handler error or panic rejects the draft too.

## Build

```
scripts/build-plugin.sh plugins/hn
```

## License

Apache-2.0.
