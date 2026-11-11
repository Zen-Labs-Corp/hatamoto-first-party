# devportal

Developer accounts for hatamoto.dev: sign in with GitHub, accept the terms,
create a free developer account and register one ed25519 signing key. The owner
can list and suspend accounts.

This plugin is **hatamoto.dev-only**: it is not in the public plugin registry and
is installed from a signed `.hmp` on the site that runs the developer portal.

## How it works

1. A visitor opens the themed CMS page `/developers`. The plugin's `render.page`
   filter replaces the page's placeholder block with the portal section for the
   visitor's state: signed out, signup, dashboard or suspended.
2. "Sign in with GitHub" is a plain link to GitHub's authorize URL. Its `state`
   equals the cookie `st` set by the filter.
3. GitHub returns to the public route `GET /p/devportal/auth/callback`. The
   plugin checks `state` against the cookie and against the marker the page filter
   stored when it minted it, spends the marker (single use), trades
   the code for a token, reads the profile and the verified emails, drops the
   token and answers `303` to `/developers`.
4. A first sign-in lands on the signup form. The account row is created only
   when the terms box is ticked. Later sign-ins find the account by GitHub id,
   never by login, and refresh the login.
5. The dashboard shows the account's one key (its 16 hex character key id, like
   `hatamoto keys generate` prints) or a form to paste a public key.

The forms are real same-origin forms that `POST` to public routes under
`/p/devportal/`. Each route requires an `Origin` equal to the `public_url`
setting and a hidden CSRF token that matches the session, and answers
`303 Location: /developers`.

## The `/developers` page

Create a themed page at `/developers` with **one block** whose `anchor` is
`devportal` (a short text block is fine; it is what visitors see if the plugin
is off). The plugin replaces that block, or the page's first block when none has
the anchor. Nothing else on the page is touched, and every other path is
returned unchanged without a host call. The section type is `devportal`
(`sections/devportal.html`).

Cost: while a plugin handles `render.page`, the host serves every public page
`private, no-store` and does not page-cache it. The hook timeout is 100 ms and
the filter fails open: on any error the placeholder block stays.

## Settings

| Key | Type | Default | What |
|---|---|---|---|
| `github_client_id` | string | empty | OAuth App client ID |
| `github_client_secret` | string, write-only | empty | OAuth App client secret; never shown or logged |
| `review_email` | string | empty | Contact shown on the terms page |
| `public_url` | string | `https://hatamoto.dev` | Site origin without a trailing slash; forms must come from it and GitHub returns to it |
| `terms_markdown` | string | a short draft | Developer terms: paragraphs separated by blank lines, `# ` lines are headings. Shown as plain text: every character is escaped, no HTML, no links |

GitHub OAuth App: callback URL `<public_url>/p/devportal/auth/callback`; the
plugin asks for the scopes `read:user user:email`.

## Permissions

| Permission | Why |
|---|---|
| `db` | accounts, keys and sessions |
| `kv` | issued-state marker for the OAuth `state` (10 minutes, one per `st` cookie) |
| `http.fetch` | GitHub token exchange, `/user`, `/user/emails` (hosts `github.com`, `api.github.com`) |
| `settings` | reads the settings above |
| `log` | failure stages only, never tokens, codes or secrets |

No `mail.send`, no `site.scripts`.

## Routes

| Route | Access | Purpose |
|---|---|---|
| `GET /auth/callback` | public | GitHub return |
| `POST /account` | public | create the account (terms ticked) |
| `POST /key` | public | register the key |
| `POST /logout` | public | end the session |
| `GET /terms` | public | the terms, read only |
| `GET /admin/accounts` | owner | list and search, 50 per page |
| `POST /admin/accounts/{id}/suspend` | owner | suspend and end the sessions |
| `POST /admin/accounts/{id}/unsuspend` | owner | lift the suspension |

Public routes are limited by the host to 10 requests per second per address.

## Data

- `accounts`: GitHub id, login, display name, one verified email, plan
  (`free`), status (`active` or `suspended`), the time the terms were accepted.
- `keys`: at most one per account; the public key is unique across accounts.
- `sessions`: the sha256 of the cookie token, never the token; 30 days sliding.
  A signup session carries the GitHub identity until the account exists. An
  hourly cron (`gc`) deletes expired rows.

The GitHub access token is used for the two reads and discarded. Only verified
emails are stored or accepted.

## Development

```
scripts/build-plugin.sh plugins/devportal
```

To try it in a local core, put the built plugin in a directory
`<dir>/devportal/` and start core with `HATAMOTO_PLUGIN_DIR=<dir>` (see the core
plugin API, "Development"). Point the manifest `config` keys
`github_authorize_url`, `github_token_url` and `github_api_url` at a fake GitHub
for tests.

## License

Apache-2.0, see `LICENSE`.
