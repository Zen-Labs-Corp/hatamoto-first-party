# hn

A Hacker News style community: stories, votes, karma, ranked threads, flags and moderation, built on core comments.

Status: 1.1.0: every phase (accounts, stories, ranked comments, moderation, parity pages, polish) is in, plus password accounts and the abuse fixes (voting-ring damping, IDN domain bans, port check, domain ban kills, network-banned votes, Origin check, Recent moderation view). Built against core v1.4.6 and SDK v0.29.0.

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
| `/` `/news` `/newest` `/front` `/ask` `/show` `/jobs` `/best` `/from` | `hn_feed` (the feed is the path) | P2 | `p`, `next`, `day` (front), `site` (from), `id` (newest by author) |
| `/submit` | `hn_submit` (edit mode with `?id=<story>`) | P2 | `id` |
| `/item/<number>` | `hn_item`, injected by the filter | P2 | `id` (a comment id: focus view) |
| `/threads` `/newcomments` | `hn_threads` | P3 | `id` (user), `p` |
| `/user` | `hn_user` | P1 | `id` |
| `/login` | `hn_login` | P1 | none |
| `/favorites` | `hn_saved` (favorites; `view=hidden` is the member's hidden stories) | P5 | `id`, `view`, `p` |
| `/search` | `hn_search` | P5 | `q`, `p` |
| every filtered page | `hn_userbar`, injected first | P1 | |

Sections: `hn_feed hn_submit hn_item hn_threads hn_user hn_login hn_saved hn_search hn_userbar` (`hn_item` and `hn_userbar` are injected, not placed). Declared page paths: 16 (the limit), prefix `/item/`. Query keys (9): `p id day site kind next view q m` (`m` is the notice code of a redirect, read by every filtered page).

The noprocrast notice is rendered by `hn_saved` as well: a member with the flag on who is past their visit window sees the account bar and that notice instead of the page (no extra section to place).

## Feeds and API

Public routes of the parity phase, served at `/p/hn/...`.

- `GET /p/hn/rss?feed=news|newest|ask|show|jobs|best` (default `news`): RSS 2.0, 30 items, root-relative links (the plugin does not know the site address), `Cache-Control: public, max-age=60`. An unknown feed is a 404.
- `GET /p/hn/api/feed/{name}?p=N` answers `{"items":[...],"page":N,"has_more":bool}`; `GET /p/hn/api/item/{number}` answers `{"id","type","title","url","text","by","points","comments","created_at"}` (`id` is the item number as a string); `GET /p/hn/api/user/{name}` answers `{"name","created_at","karma","about"}`. An unknown feed, item or user is a 404 `{"error":"not found"}`; success sends `Cache-Control: public, max-age=30`. The API is read-only and shows only what a signed-out visitor sees (no dead or hidden items, no keys, no emails). Field names are final once released.
- Favorites and hide: `POST /p/hn/favorite/{story|comment}/{id}` and `POST /p/hn/unfavorite/{story|comment}/{id}` (a story id is the item number, a comment id is the core comment id), `POST /p/hn/hide/{number}` and `POST /p/hn/unhide/{number}`. Story rows get the hide and favorite actions through the row-action seam, and feed and item rows add `past` (a link to `/search?q=<title>`) between them (`[unvote |] hide | past | favorite`; the `/favorites` and `/search` lists do not show it); a theme may print the comment favorite form itself.
- Search: a full-text index of story titles and text and of comment text, kept by story and comment events and repaired by the `search` cron (every 5 minutes).

## User bar and the theme header

hn adds one block of type `hn_userbar` (id `hn_userbar`) first in the blocks of every page it filters. Its section renders one `<div class="hn-ub">`: a login link, or the name, karma and a logout button in an inline POST form (`.hn-out`).

Theme contract:

- Recommended, server-side and without JavaScript: the theme renders the block in its header with `{{range .Blocks}}{{if eq .Type "hn_userbar"}}{{template "section" .}}{{end}}{{end}}` and skips that type wherever it loops over `.Blocks` for the main content. Use core's `section` template, never `{{template "section-hn_userbar" .}}` or a `plugin/hn/...` partial by name: those fail the page on a site without hn.
- Without that, the bar stays at the top of `main`. That is the no-script rendering and it must stay usable; the theme should not hide it.
- With `site.scripts` on, `assets/plugin.js` moves the first `.hn-ub` into the first `.hn-userbar` element (for example an empty `<span class="hn-userbar"></span>` in the header), once, and leaves it alone when it is already inside one, at any depth. Registry installs never run the script (Hatamoto does not grant `site.scripts` to a community package), so do not depend on it.
- The bar can therefore be in either place. A theme that styles `.hn-ub` (spacing, background, alignment, colours) should make that styling work in both positions, for example by scoping the rules to each position. The plugin's own rules are zero-specificity, use the `--hn-*` tokens and set no text colour on `.hn-ub`, so it inherits the colour of its container.

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
| `POST /login`, `POST /signup`, `POST /logout`, `POST /logout/all`, `POST /password`, `POST /password/connect` | public, same-origin browser posts only | P1 |
| `POST /submit`, `POST /story/{id}/edit`, `POST /story/{id}/delete`, `POST /vote/story/{id}` | public | P2 |
| `POST /admin/jobs` | admin | P2 |
| `POST /vote/comment/{id}` | public | P3 |
| `POST /flag/{kind}/{id}`, `POST /vouch/{kind}/{id}` | public | P4 |
| `GET /admin/queue`, `GET /admin/log`, `POST /admin/users/{name}/ban`, `POST /admin/users/{name}/unban`, `POST /admin/users/{name}/password/remove`, `POST /admin/domains`, `POST /admin/domains/remove`, `POST /admin/nets/ban`, `POST /admin/nets/unban`, `POST /admin/items/kill`, `POST /admin/items/restore` | admin | P4 |
| `POST /favorite/{kind}/{id}`, `POST /unfavorite/{kind}/{id}`, `POST /hide/{id}`, `POST /unhide/{id}` | public | P5 |
| `GET /rss`, `GET /api/feed/{name}`, `GET /api/item/{id}`, `GET /api/user/{name}` | public | P5 |
| `GET /state/{entity_id}` | public | P6 |

All six account routes accept only same-origin browser posts (`Sec-Fetch-Site: same-origin`): a sibling subdomain, a cross-site post or a request with no Fetch Metadata is refused with 403 before anything happens, because core signs a browser in only on same-origin requests.

## Accounts and passwords

A member is a username and a password, no email. The login page has two forms, Login and Create Account.

- Email is optional, set on the own profile of a password member. It is never shown and used by nothing yet: password reset by email is not in this release, and "Forgot your password?" is not rendered.
- Passwords are 8 to 72 bytes and stored only as bcrypt hashes (cost 11, about 180 ms per hash in wasm), in `user_logins` (see [SCHEMA.md](SCHEMA.md)), never in `users`. A hash made at a lower cost is replaced on the next login.
- A failed login answers the same "Bad login." for an unknown user and a wrong password, and both run one bcrypt compare (an unknown user against a dummy hash of the same cost), so neither the answer nor the time tells them apart. Create Account says "That username is taken." for a taken name.
- Limits, counted per hour and checked before any hash is computed: sign-up 3 per address and 5 per network; login 20 per address, 60 per network, 10 per username and network, and 100 per username overall (attempts, right or wrong), and 10 password changes or connects per member. The address and network limits need the `client.key` grant; without it all requests share one site-wide limit (20 sign-ups and 120 logins per hour), so a flood is capped but also blocks everyone.
- Logout is a POST (the `.hn-out` form in the user bar) that clears the core sign-in cookie. "Log out everywhere" on the own profile signs every browser of the account out.
- A member who signed up with the email link connects a password with the separate "connect a password" form on their profile (they type their username and the new password twice; core gives no proof of who holds the browser, so a stolen signed-in browser can still connect a password, which the owner can undo with `POST /admin/users/{name}/password/remove`: it signs the account out and deletes the login) and keeps their username, comments and karma. This is the only action that joins a browser's existing sign-in to a password account; Create Account and Login never do. Changing a password needs the current one and signs the other browsers out.

### Uninstalling and reinstalling

Uninstalling hn signs every member out on every device (password members and email-link members who connected a password) and detaches them from hn; comments stay. Removing only the `comments.identity` grant signs nobody out; use Admin > Settings > Comments > Commenters to sign members out. After a reinstall that kept hn's data, members log in again: a password member gets a new core commenter and hn moves the username to it at that login. Comments written before the uninstall stay with the old commenter and no longer show under the username. Choosing "delete its data too" removes every hn account.

## Cron

| Job | Every | Phase | Does |
|---|---|---|---|
| `rank` | 1m | P2 | gravity rank of stories |
| `comment_rank` | 1m | P3 | writes the comment order in batches |
| `reconcile` | 1h | P3 | orphan sweep, karma recount |
| `sweep` | 1h | P4 | expires bans, trims rate events |
| `search` | 5m | P5 | search index reconcile |

## Permissions

1.1.0 adds `comments.identity`: existing installs tick it on update. Adding a permission later makes every install grant again.

| Permission | Used by | Why |
|---|---|---|
| `db` | all | votes, karma, ranks, lists, flags and the search mirror, in the plugin's own database area |
| `settings` | all | the thresholds below |
| `site.scripts` | P6 | `assets/plugin.js`: votes without a reload, a multi-line submit text box (without the grant the host refuses `<textarea>` in a section, so the text is a one-line input); optional: everything works without it, and nothing in these docs asks you to grant it (Hatamoto never grants it to a community package) |
| `entities.write.own` | P2 | creates, edits and deletes stories of the one content type `hn-story` |
| `comments.read` | P1, P3, P4, P5 | comment events, a member's comments, the commenter key on pages and routes |
| `comments.moderate` | P1, P3, P4 | marks comments dead or restores them, sets their order |
| `client.key` | P1, P4 | pseudonymous address and network keys, for the login and sign-up rate limits and for network bans |
| `comments.identity` | P1 | signs members in as core commenters for accounts the plugin manages (no email); needs `comments.read` and the content type, both declared |

In plain words, the four that warn on install:

- Comments, moderate: marks comments on the plugin's own content type dead or restores them and sets their order, without review. It cannot post comments or touch other content types. Logged in the audit log.
- Content, write (own type): creates, edits and deletes entries of one content type it declares and publishes them at once, without review. Rate limited and logged. It cannot touch any other content.
- Visitor keys: a pseudonymous key for each visitor's address and network, not the address itself. The key is stable, so the plugin can recognise a returning visitor and block a network. It is personal data: your privacy notice should say so.
- Comments, identity: signs visitors in as commenters the plugin vouches for (a username and password, no email) and signs them out again. It can only act on accounts the plugin created itself. Needs core v1.4.6 or newer.

No `http.fetch`, `kv`, `mail.send`, `log` or `entities.read`: the lists read the plugin's own copy of each story.

`request_headers` declares `sec-fetch-site` and the always-passed `Origin` header (not permissions): routes use them to refuse cross-site form posts.

## Settings

| Setting | Default | Meaning |
|---|---|---|
| `flag_karma` | 30 | karma needed to flag (nobody has it on a new site: see Moderation and abuse controls) |
| `downvote_karma` | 500 | karma needed to downvote a comment |
| `vouch_karma` | 31 | karma needed to vouch for a dead item |
| `edit_window_minutes` | 120 | how long an author may edit or delete a story |
| `submit_per_hour` | 6 | stories one member may submit per hour |
| `flags_to_dead` | 5 | flags after which an item is marked dead |
| `page_size` | 30 | stories per page (1 to 100) |

## Moderation and abuse controls

- **Flags and karma on a new site.** New members start at karma 1 and flagging needs `flag_karma` (30), so nobody can flag at first. The owner kills spam from the **Recent** tab of the News moderation page (the newest 50 stories and 50 comments, live or dead, with kill, restore and ban). The default stays at 30; lower it in the plugin settings for the first weeks if you prefer.
- **Queue.** Flagged items first, then every dead item with its cause (flags, owner, ban or domain), so anything killed can be restored from the page.
- **Voting rings.** One network adds at most 2 counted up-votes to one story per 24 hours. Later ones are stored uncounted: the voter still sees "voted", but the vote adds no points, rank or karma, and each one is logged as `ring-vote` in the Log tab. Nothing is deleted. Needs the `client.key` permission (no network key, no check).
- **Site bans.** A banned site includes its subdomains. A unicode spelling is stored and matched as punycode (new stories from an internationalised site show the punycode host). Banning kills the site's live stories (cause `domain`, members cannot vouch for them); removing the ban brings them back unless another ban still covers them.
- **Network bans.** A banned network cannot submit or vote, and its comments are held.
- **Story links.** Links must be http or https, with a port of 1 to 65535 if any.
- **Cross-site posts.** Posts are refused by Fetch Metadata (`Sec-Fetch-Site`); a request that has an `Origin` but no `Sec-Fetch-Site` is refused too.
- **Held comments.** The reasons shown are "blocked member", "blocked network" and "banned site".

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
